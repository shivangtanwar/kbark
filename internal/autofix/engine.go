// SPDX-License-Identifier: Apache-2.0

// Package autofix is the operations loop: watch every cluster, open an
// incident for each detected failure, plan a fix, wait for a person to
// approve it, apply it, and verify that the failure cleared.
//
// The human-in-the-loop rule lives here. The only code path that calls
// remediate.Apply is Approve, and Approve requires a named operator.
package autofix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/policy"
	"github.com/shivangtanwar/kbark/internal/remediate"
)

// Config tunes the engine. Zero values get defaults from New.
type Config struct {
	// Interval is how often every cluster is scanned.
	Interval time.Duration
	// VerifyTimeout is how long an applied fix has to clear the failure.
	VerifyTimeout time.Duration
	// VerifyHold is how long the failure must stay clear to count.
	VerifyHold time.Duration
	// Policy decides per cluster and namespace what may be proposed.
	// When nil, one is built from Protected and MaxAttempts.
	Policy *policy.Policy
	// Protected and MaxAttempts seed the default policy when Policy is nil.
	Protected   []string
	MaxAttempts int
	Planner     *remediate.Planner
	// Explainer may be nil: detection and fixes do not depend on it.
	Explainer *Explainer
	// StatePath, when set, is where incidents and trend samples are
	// saved so they survive a restart.
	StatePath string
	// SampleEvery is how often a trend sample is recorded.
	SampleEvery time.Duration
	// ReplanEvery is how often an incident with no automatic fix is
	// planned again, in case the situation has changed.
	ReplanEvery time.Duration
}

// DefaultProtected are the namespaces kbark never proposes changes in.
var DefaultProtected = []string{"kube-system", "kube-public", "kube-node-lease", "local-path-storage"}

// ErrOperatorRequired is returned when a write is requested without a name.
var ErrOperatorRequired = errors.New("an operator name is required")

// errUnchanged aborts a store update when re-planning reached the same
// conclusion as before.
var errUnchanged = errors.New("plan unchanged")

// ErrRefreshed is returned when approval was refused because the target
// changed; the proposal has been recomputed and needs a fresh look.
var ErrRefreshed = errors.New("the target changed since this fix was proposed; the proposal has been refreshed, review it again")

// Engine runs the loop.
type Engine struct {
	cfg      Config
	store    *incident.Store
	clusters map[string]*fleet.Cluster
	order    []string

	mu         sync.RWMutex
	snaps      map[string]fleet.Snapshot
	samples    []Sample
	lastSample time.Time
	lastSaved  []byte
	// retried records when each no-fix incident was last re-planned.
	retried map[string]time.Time

	bg     context.Context
	wg     sync.WaitGroup
	notify func()
}

// New builds an engine over the given clusters.
func New(clusters []*fleet.Cluster, store *incident.Store, cfg Config, notify func()) *Engine {
	if cfg.Interval == 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.VerifyTimeout == 0 {
		cfg.VerifyTimeout = 150 * time.Second
	}
	if cfg.VerifyHold == 0 {
		cfg.VerifyHold = 15 * time.Second
	}
	if cfg.Policy == nil {
		protected := cfg.Protected
		if protected == nil {
			protected = DefaultProtected
		}
		cfg.Policy = policy.Default(protected, "2Gi")
		if cfg.MaxAttempts > 0 {
			cfg.Policy.Defaults.MaxAttempts = cfg.MaxAttempts
		}
	}
	if cfg.SampleEvery == 0 {
		cfg.SampleEvery = 30 * time.Second
	}
	if cfg.ReplanEvery == 0 {
		cfg.ReplanEvery = 30 * time.Second
	}
	if notify == nil {
		notify = func() {}
	}
	e := &Engine{
		cfg: cfg, store: store, clusters: map[string]*fleet.Cluster{},
		snaps: map[string]fleet.Snapshot{}, retried: map[string]time.Time{}, bg: context.Background(), notify: notify,
	}
	for _, c := range clusters {
		e.clusters[c.Name] = c
		e.order = append(e.order, c.Name)
	}
	return e
}

// Run scans until ctx ends. Incidents that were mid-verification when
// a previous process stopped are picked up again.
func (e *Engine) Run(ctx context.Context) {
	e.bg = ctx
	for _, inc := range e.store.List() {
		if inc.State == incident.StateApplying && inc.Action != nil && e.clusters[inc.Cluster] != nil {
			id := inc.ID
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				e.verify(id)
			}()
		}
	}
	t := time.NewTicker(e.cfg.Interval)
	defer t.Stop()
	save := time.NewTicker(5 * time.Second)
	defer save.Stop()
	for {
		e.Tick(ctx, time.Now())
		select {
		case <-ctx.Done():
			e.wg.Wait()
			_ = e.Save()
			return
		case <-save.C:
			_ = e.Save()
		case <-t.C:
		}
	}
}

// Policy returns the active policy.
func (e *Engine) Policy() *policy.Policy { return e.cfg.Policy }

// Tick performs one scan of every cluster: detect, open or resolve
// incidents, plan new ones, and refresh proposals that went stale.
func (e *Engine) Tick(ctx context.Context, now time.Time) {
	for _, name := range e.order {
		c := e.clusters[name]
		snap := c.Scan(now)

		e.mu.Lock()
		prev, had := e.snaps[name]
		e.snaps[name] = snap
		e.mu.Unlock()
		if !had || prev.Summary != snap.Summary {
			e.notify()
		}
		if !c.Synced() {
			continue
		}
		for _, inc := range e.store.Observe(name, snap.Observations, now) {
			e.plan(ctx, inc.ID, "kbark")
		}
	}
	for _, inc := range e.store.List() {
		c := e.clusters[inc.Cluster]
		if c == nil || !c.Synced() {
			continue
		}
		switch {
		case inc.State == incident.StateDetected:
			// Restored from disk before it was planned.
			e.plan(ctx, inc.ID, "kbark")
		case inc.State == incident.StateProposed && inc.Action != nil && remediate.Stale(c, inc.Namespace, inc.Action):
			e.plan(ctx, inc.ID, "kbark")
		case inc.State == incident.StateManual && e.observed(inc.Cluster, inc.Key) && e.dueForRetry(inc.ID, now):
			// "No automatic fix" can stop being true: a workload that
			// was mid-recreation exists again, a new revision gives a
			// rollback target, an attempt window expires. Plan again,
			// quietly unless the answer changes.
			e.plan(ctx, inc.ID, "kbark")
		case !inc.ExplainStarted && (inc.State == incident.StateProposed || inc.State == incident.StateManual):
			e.explain(inc)
		}
	}
	e.sample(now)
}

// Snapshots returns the latest scan of each cluster, in configured order.
func (e *Engine) Snapshots() []fleet.Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]fleet.Snapshot, 0, len(e.order))
	for _, name := range e.order {
		if s, ok := e.snaps[name]; ok {
			out = append(out, s)
		} else {
			out = append(out, fleet.Snapshot{Summary: fleet.Summary{Name: name, State: "connecting"}})
		}
	}
	return out
}

func (e *Engine) observed(cluster, key string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, o := range e.snaps[cluster].Observations {
		if o.Key == key {
			return true
		}
	}
	return false
}

// dueForRetry reports whether a no-fix incident should be planned
// again now, and records the attempt.
func (e *Engine) dueForRetry(id string, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	last, ok := e.retried[id]
	if !ok {
		// First sighting: start the clock rather than re-plan at once.
		e.retried[id] = now
		return false
	}
	if now.Sub(last) < e.cfg.ReplanEvery {
		return false
	}
	e.retried[id] = now
	return true
}

// plan computes the proposal (or the reason there is none) for an
// incident and moves it to Proposed or Manual.
func (e *Engine) plan(ctx context.Context, id, actor string) {
	inc, err := e.store.Get(id)
	if err != nil {
		return
	}
	c := e.clusters[inc.Cluster]
	if c == nil {
		return
	}
	now := time.Now()

	d := e.cfg.Policy.For(inc.Cluster, inc.Namespace)
	var p remediate.Plan
	switch {
	case d.DetectOnly:
		p.Manual = fmt.Sprintf("Namespace %q is protected by policy (%s): kbark reports failures here but never proposes changes.", inc.Namespace, d.Rule)
	case e.store.Attempts(inc.Cluster, inc.Namespace, inc.Workload, now.Add(-d.AttemptWindow)) >= d.MaxAttempts:
		p.Manual = fmt.Sprintf("kbark has already applied %d fixes to %s within %s and the workload is failing again. Further automatic changes are stopped; a person should look at it.",
			d.MaxAttempts, inc.Workload, d.AttemptWindow)
	default:
		planner := *e.cfg.Planner
		planner.MemoryCeiling = d.MemoryCeiling
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		p = planner.Plan(pctx, c, inc)
		cancel()
		if p.Action != nil && !d.Allows(p.Action.Type) {
			p = remediate.Plan{Manual: fmt.Sprintf("Policy (%s) does not allow the action %q in this namespace. kbark would otherwise have proposed: %s.", d.Rule, p.Action.Type, p.Action.Title)}
		}
	}

	updated, err := e.store.Update(id, now, func(i *incident.Incident) (incident.State, string, string, error) {
		switch i.State {
		case incident.StateDetected, incident.StateProposed, incident.StateManual, incident.StateRejected:
		default:
			return "", "", "", incident.ErrWrongState
		}
		if p.Action != nil {
			if i.State == incident.StateManual {
				// The explanation was written for "no fix"; it no
				// longer describes what is on screen.
				i.ExplainStarted, i.Explanation, i.ExplanationState, i.ExplanationNote = false, "", incident.ExplainPending, ""
			}
			i.Action, i.ManualReason = p.Action, ""
			return incident.StateProposed, actor, p.Action.Title, nil
		}
		if i.State == incident.StateManual && i.ManualReason == p.Manual {
			// Nothing changed: leave the record and its history alone.
			return "", "", "", errUnchanged
		}
		i.Action, i.ManualReason = nil, p.Manual
		return incident.StateManual, actor, p.Manual, nil
	})
	if err == nil && !updated.ExplainStarted {
		e.explain(updated)
	}
}

// Approve applies the proposed fix on behalf of a named operator and
// starts verification. This is the only path to a cluster write.
func (e *Engine) Approve(ctx context.Context, id, operator string) (*incident.Incident, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, ErrOperatorRequired
	}
	// Claim the incident first so two approvals cannot both apply.
	inc, err := e.store.Update(id, time.Now(), func(i *incident.Incident) (incident.State, string, string, error) {
		if i.State != incident.StateProposed || i.Action == nil {
			return "", "", "", incident.ErrWrongState
		}
		return incident.StateApplying, operator, "approved: " + i.Action.Title, nil
	})
	if err != nil {
		return nil, err
	}
	c := e.clusters[inc.Cluster]

	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	undo, err := remediate.Apply(actx, c, inc.Namespace, inc.Action)
	cancel()

	if errors.Is(err, remediate.ErrPrecondition) {
		_, _ = e.store.Update(id, time.Now(), func(*incident.Incident) (incident.State, string, string, error) {
			return incident.StateProposed, "kbark", "not applied: the target changed after the proposal was shown", nil
		})
		e.plan(ctx, id, "kbark")
		return nil, ErrRefreshed
	}
	if err != nil {
		_, _ = e.store.Update(id, time.Now(), func(*incident.Incident) (incident.State, string, string, error) {
			return incident.StateFailed, "kbark", "the change could not be applied: " + err.Error(), nil
		})
		return nil, fmt.Errorf("apply: %w", err)
	}

	inc, err = e.store.Update(id, time.Now(), func(i *incident.Incident) (incident.State, string, string, error) {
		i.Undo = undo
		i.CanRollback = undo != nil && i.Action.Reversible
		return "", "", "", nil
	})
	if err != nil {
		return nil, err
	}
	e.store.Note(id, "applied", operator, incident.AppliedPrefix+inc.Action.Title, time.Now())

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.verify(id)
	}()
	return e.store.Get(id)
}

// verify waits for the cluster to settle and the failure to stay clear.
func (e *Engine) verify(id string) {
	inc, err := e.store.Get(id)
	if err != nil {
		return
	}
	c := e.clusters[inc.Cluster]
	hold := remediate.HoldFor(inc.Action, e.cfg.VerifyHold)
	// A longer hold gets the same extra time to finish in.
	timeout := e.cfg.VerifyTimeout + (hold - e.cfg.VerifyHold)
	deadline := time.Now().Add(timeout)
	var clearSince time.Time
	waiting := ""

	t := time.NewTicker(min(e.cfg.Interval, time.Second))
	defer t.Stop()
	for {
		select {
		case <-e.bg.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		settled, why := remediate.Settled(c, inc.Namespace, inc.Action)
		switch {
		case !settled:
			waiting, clearSince = why, time.Time{}
		case e.observed(inc.Cluster, inc.Key):
			waiting, clearSince = "the failure is still observed", time.Time{}
		case clearSince.IsZero():
			clearSince = now
		case now.Sub(clearSince) >= hold:
			_, _ = e.store.Update(id, now, func(i *incident.Incident) (incident.State, string, string, error) {
				return incident.StateVerified, "kbark", fmt.Sprintf("verified: the failure cleared and stayed clear for %s; recovered %s after detection",
					hold, now.Sub(i.OpenedAt).Round(time.Second)), nil
			})
			return
		}
		if now.After(deadline) {
			_, _ = e.store.Update(id, now, func(i *incident.Incident) (incident.State, string, string, error) {
				note := fmt.Sprintf("not verified after %s: %s", timeout, waiting)
				if i.CanRollback {
					note += ". The fix can be rolled back."
				}
				return incident.StateFailed, "kbark", note, nil
			})
			return
		}
	}
}

// Reject records that a person declined the proposal. Nothing is written
// to the cluster.
func (e *Engine) Reject(id, operator, reason string) (*incident.Incident, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, ErrOperatorRequired
	}
	note := "rejected"
	if r := strings.TrimSpace(reason); r != "" {
		note += ": " + r
	}
	return e.store.Update(id, time.Now(), func(i *incident.Incident) (incident.State, string, string, error) {
		if i.State != incident.StateProposed {
			return "", "", "", incident.ErrWrongState
		}
		return incident.StateRejected, operator, note, nil
	})
}

// Replan recomputes the proposal for an incident that was rejected or
// had no automatic fix, for when circumstances have changed.
func (e *Engine) Replan(ctx context.Context, id, operator string) (*incident.Incident, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, ErrOperatorRequired
	}
	inc, err := e.store.Get(id)
	if err != nil {
		return nil, err
	}
	switch inc.State {
	case incident.StateProposed, incident.StateManual, incident.StateRejected:
	default:
		return nil, incident.ErrWrongState
	}
	e.plan(ctx, id, operator)
	return e.store.Get(id)
}

// Rollback undoes an applied fix on behalf of a named operator.
func (e *Engine) Rollback(ctx context.Context, id, operator string) (*incident.Incident, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, ErrOperatorRequired
	}
	inc, err := e.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !inc.CanRollback || inc.Undo == nil || (inc.State != incident.StateVerified && inc.State != incident.StateFailed) {
		return nil, incident.ErrWrongState
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = remediate.Rollback(rctx, e.clusters[inc.Cluster], inc.Namespace, inc.Action, inc.Undo)
	cancel()
	if errors.Is(err, remediate.ErrPrecondition) {
		return nil, errors.New("the workload has been changed since the fix was applied, so kbark will not overwrite it")
	}
	if err != nil {
		return nil, fmt.Errorf("rollback: %w", err)
	}
	return e.store.Update(id, time.Now(), func(i *incident.Incident) (incident.State, string, string, error) {
		i.CanRollback = false
		return incident.StateRolledBack, operator, "rolled back: restored the state from before \"" + i.Action.Title + "\"", nil
	})
}
