// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shivangtanwar/kbark/internal/fleet"
)

// ResolveAfter is how long a failure must stay unobserved before its
// incident is closed as resolved. Crash-looping pods look healthy for a
// few seconds on every restart; this keeps the incident from flapping.
const ResolveAfter = 45 * time.Second

// maxClosed bounds how many closed incidents are kept for the dashboard.
const maxClosed = 200

// AppliedPrefix starts the history note written when a fix has been
// written to the cluster. Attempts counts these.
const AppliedPrefix = "applied: "

// ErrNotFound is returned for an unknown incident ID.
var ErrNotFound = errors.New("incident not found")

// ErrWrongState is returned when an operation does not apply to the
// incident's current state (approving something already applied, say).
var ErrWrongState = errors.New("incident is not in a state that allows this")

// Store is the in-memory incident table. Safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	seq    int
	byID   map[string]*Incident
	open   map[string]*Incident // cluster + "|" + key -> open incident
	audit  *Audit
	notify func()
	// onEvent, when set, receives every audit entry as it is recorded
	// (for notifications). Called with the store lock held; it must
	// not block or call back into the store.
	onEvent func(Entry)
}

// OnEvent registers the event hook. Call before the store is used.
func (s *Store) OnEvent(fn func(Entry)) { s.onEvent = fn }

// Saved is one incident with the internal fields JSON normally hides,
// so a restart can still roll an applied fix back.
type Saved struct {
	Incident       *Incident `json:"incident"`
	Undo           *Undo     `json:"undo,omitempty"`
	Template       []byte    `json:"template,omitempty"`
	ExplainStarted bool      `json:"explainStarted,omitempty"`
}

// Dump is the store's full state for persistence.
type Dump struct {
	Seq   int     `json:"seq"`
	Items []Saved `json:"items"`
}

// Dump returns a deep copy of everything in the store.
func (s *Store) Dump() Dump {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Dump{Seq: s.seq}
	for _, inc := range s.byID {
		c := inc.clone()
		sv := Saved{Incident: c, Undo: inc.Undo, ExplainStarted: inc.ExplainStarted}
		if inc.Action != nil {
			sv.Template = inc.Action.Template
		}
		d.Items = append(d.Items, sv)
	}
	sort.Slice(d.Items, func(i, j int) bool { return d.Items[i].Incident.ID < d.Items[j].Incident.ID })
	return d
}

// Load replaces the store's contents with a previous Dump.
func (s *Store) Load(d Dump) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq = d.Seq
	s.byID, s.open = map[string]*Incident{}, map[string]*Incident{}
	for _, sv := range d.Items {
		inc := sv.Incident
		if inc == nil || inc.ID == "" {
			continue
		}
		inc.Undo, inc.ExplainStarted = sv.Undo, sv.ExplainStarted
		if inc.Action != nil {
			inc.Action.Template = sv.Template
		}
		s.byID[inc.ID] = inc
		if inc.State.Open() {
			s.open[openKey(inc.Cluster, inc.Key)] = inc
		}
	}
}

// NewStore builds a store. audit may be nil; notify (may be nil) is
// called after every change so the server can push an update.
func NewStore(audit *Audit, notify func()) *Store {
	if notify == nil {
		notify = func() {}
	}
	return &Store{byID: map[string]*Incident{}, open: map[string]*Incident{}, audit: audit, notify: notify}
}

func openKey(cluster, key string) string { return cluster + "|" + key }

// Observe folds one cluster scan into the store. It opens incidents
// for new failures, refreshes evidence on open ones, and resolves
// incidents whose failure has been gone for ResolveAfter. It returns
// the newly opened incidents.
func (s *Store) Observe(cluster string, obs []fleet.Observation, now time.Time) []*Incident {
	s.mu.Lock()
	var created []*Incident
	changed := false
	seen := map[string]bool{}

	for _, o := range obs {
		seen[o.Key] = true
		if inc, ok := s.open[openKey(cluster, o.Key)]; ok {
			inc.LastSeen = now
			// Evidence follows the live state while a person may still
			// be deciding. Once a fix is in flight the record is frozen
			// so the audit trail shows what was approved.
			if inc.State != StateApplying {
				if inc.Summary != o.Finding.Summary || len(inc.Pods) != len(o.Pods) || !sameEvidence(inc, o) {
					inc.Summary, inc.Reason, inc.Cause = o.Finding.Summary, o.Finding.Reason, o.Finding.Cause
					inc.Evidence, inc.Pods = o.Finding.Evidence, o.Pods
					inc.Container, inc.Init, inc.Probe = o.Finding.Container, o.Finding.Init, o.Finding.Probe
					inc.UpdatedAt = now
					changed = true
				}
			}
			continue
		}
		s.seq++
		inc := &Incident{
			ID:      fmt.Sprintf("INC-%04d", s.seq),
			Cluster: cluster, Namespace: o.Namespace, Workload: o.Workload, Key: o.Key,
			Family: o.Finding.Family, Reason: o.Finding.Reason, Cause: o.Finding.Cause,
			Summary: o.Finding.Summary, Container: o.Finding.Container, Init: o.Finding.Init, Probe: o.Finding.Probe,
			Evidence: o.Finding.Evidence, Pods: o.Pods,
			State: StateDetected, ExplanationState: ExplainPending,
			OpenedAt: now, UpdatedAt: now, LastSeen: now,
		}
		inc.History = []HistoryEntry{{Time: now, State: StateDetected, Actor: "kbark", Note: o.Finding.Summary}}
		s.byID[inc.ID] = inc
		s.open[openKey(cluster, o.Key)] = inc
		s.record(inc, "detected", "kbark", o.Finding.Summary, now)
		created = append(created, inc.clone())
		changed = true
	}

	for k, inc := range s.open {
		if inc.Cluster != cluster || seen[inc.Key] || inc.State == StateApplying {
			continue
		}
		if now.Sub(inc.LastSeen) >= ResolveAfter {
			s.transition(inc, StateResolved, "kbark", "the failure is no longer observed", now)
			delete(s.open, k)
			changed = true
		}
	}
	s.trim()
	s.mu.Unlock()
	if changed {
		s.notify()
	}
	return created
}

func sameEvidence(inc *Incident, o fleet.Observation) bool {
	if len(inc.Evidence) != len(o.Finding.Evidence) {
		return false
	}
	for i := range inc.Evidence {
		if inc.Evidence[i] != o.Finding.Evidence[i] {
			return false
		}
	}
	return true
}

// transition must be called with the lock held.
func (s *Store) transition(inc *Incident, to State, actor, note string, now time.Time) {
	inc.State = to
	inc.UpdatedAt = now
	inc.History = append(inc.History, HistoryEntry{Time: now, State: to, Actor: actor, Note: note})
	if !to.Open() {
		t := now
		inc.ClosedAt = &t
		delete(s.open, openKey(inc.Cluster, inc.Key))
	}
	s.record(inc, string(to), actor, note, now)
}

func (s *Store) record(inc *Incident, event, actor, note string, now time.Time) {
	if s.audit == nil && s.onEvent == nil {
		return
	}
	e := Entry{
		Time: now, Incident: inc.ID, Cluster: inc.Cluster, Namespace: inc.Namespace,
		Workload: inc.Workload.String(), Family: string(inc.Family), Event: event, Actor: actor, Note: note,
	}
	if inc.Action != nil && event != "detected" && event != "manual" {
		e.Action, e.Target = inc.Action.Type, inc.Action.TargetKind+"/"+inc.Action.TargetName
		if event == "approved" || event == "proposed" {
			e.Diff = inc.Action.Diff
		}
	}
	if s.audit != nil {
		s.audit.Append(e)
	}
	if s.onEvent != nil {
		s.onEvent(e)
	}
}

func (s *Store) trim() {
	var closed []*Incident
	for _, inc := range s.byID {
		if inc.ClosedAt != nil {
			closed = append(closed, inc)
		}
	}
	if len(closed) <= maxClosed {
		return
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].ClosedAt.Before(*closed[j].ClosedAt) })
	for _, inc := range closed[:len(closed)-maxClosed] {
		delete(s.byID, inc.ID)
	}
}

// Get returns a copy of one incident.
func (s *Store) Get(id string) (*Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inc, ok := s.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return inc.clone(), nil
}

// List returns copies of all incidents, open ones first, newest first.
func (s *Store) List() []*Incident {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Incident, 0, len(s.byID))
	for _, inc := range s.byID {
		out = append(out, inc.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.ClosedAt == nil) != (b.ClosedAt == nil) {
			return a.ClosedAt == nil
		}
		if !a.OpenedAt.Equal(b.OpenedAt) {
			return a.OpenedAt.After(b.OpenedAt)
		}
		return a.ID > b.ID
	})
	return out
}

// Update applies fn to an incident under the lock. fn returns the new
// state (or "" to keep the current one), the actor and a note for the
// history; returning an error leaves the incident untouched.
func (s *Store) Update(id string, now time.Time, fn func(inc *Incident) (to State, actor, note string, err error)) (*Incident, error) {
	s.mu.Lock()
	inc, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	to, actor, note, err := fn(inc)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if to != "" {
		s.transition(inc, to, actor, note, now)
	} else {
		inc.UpdatedAt = now
	}
	out := inc.clone()
	s.mu.Unlock()
	s.notify()
	return out, nil
}

// Note appends an audit-only event (for example "approved") without
// changing state. The incident must exist.
func (s *Store) Note(id, event, actor, note string, now time.Time) {
	s.mu.Lock()
	if inc, ok := s.byID[id]; ok {
		inc.History = append(inc.History, HistoryEntry{Time: now, State: inc.State, Actor: actor, Note: note})
		s.record(inc, event, actor, note, now)
	}
	s.mu.Unlock()
	s.notify()
}

// Attempts counts fixes applied to a workload since the given time,
// across all of its incidents. Used to stop kbark from trying the same
// workload over and over.
func (s *Store) Attempts(cluster, namespace string, workload fleet.Ref, since time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, inc := range s.byID {
		if inc.Cluster != cluster || inc.Namespace != namespace || inc.Workload != workload {
			continue
		}
		for _, h := range inc.History {
			if strings.HasPrefix(h.Note, AppliedPrefix) && h.Time.After(since) {
				n++
			}
		}
	}
	return n
}
