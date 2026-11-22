// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
)

// Standing is a standing approval: a named person's decision that one
// kind of low-risk fix may be applied to one workload without asking
// again. It is the only way a fix is applied without a click, and it
// exists only where policy allows it (it is off by default).
//
// It is deliberately narrow: one cluster, one namespace, one workload,
// one action type; it expires; and it is revoked the first time a fix
// applied under it fails verification.
type Standing struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace"`
	Workload  fleet.Ref `json:"workload"`
	Action    string    `json:"action"`

	GrantedBy string    `json:"grantedBy"`
	GrantedAt time.Time `json:"grantedAt"`
	// GrantedOn is the incident whose approval created it.
	GrantedOn string    `json:"grantedOn"`
	ExpiresAt time.Time `json:"expiresAt"`

	Uses         int        `json:"uses"`
	LastUsed     *time.Time `json:"lastUsed,omitempty"`
	LastIncident string     `json:"lastIncident,omitempty"`

	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	RevokedBy    string     `json:"revokedBy,omitempty"`
	RevokeReason string     `json:"revokeReason,omitempty"`
}

// Active reports whether the approval can still be used at now.
func (s *Standing) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// Actor is the name recorded against a fix applied under the approval.
// It names the person, because the decision was theirs.
func (s *Standing) Actor() string {
	return fmt.Sprintf("%s (standing approval %s)", s.GrantedBy, s.ID)
}

// Errors for standing approvals.
var (
	// ErrStandingNotAllowed: policy does not permit a standing approval
	// for this fix.
	ErrStandingNotAllowed = errors.New("policy does not allow a standing approval for this fix: they are off by default, and only low-risk fixes qualify")
	ErrStandingNotFound   = errors.New("no such standing approval")
)

// Standings returns every standing approval, active ones first, then
// newest first.
func (e *Engine) Standings() []Standing {
	now := time.Now()
	e.mu.RLock()
	out := make([]Standing, 0, len(e.standing))
	for _, s := range e.standing {
		out = append(out, *s)
	}
	e.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Active(now), out[j].Active(now); a != b {
			return a
		}
		return out[i].GrantedAt.After(out[j].GrantedAt)
	})
	return out
}

// standingAllowed reports whether policy permits a standing approval
// for this incident's proposed action.
func (e *Engine) standingAllowed(inc *incident.Incident) bool {
	if inc.Action == nil || inc.Action.Risk != "low" {
		return false
	}
	d := e.cfg.Policy.For(inc.Cluster, inc.Namespace)
	return d.Standing && !d.DetectOnly && d.Allows(inc.Action.Type)
}

// grant records a standing approval for the incident's action. An
// existing active one for the same target is extended instead.
func (e *Engine) grant(inc *incident.Incident, operator string, now time.Time) Standing {
	ttl := e.cfg.Policy.For(inc.Cluster, inc.Namespace).StandingTTL
	e.mu.Lock()
	var s *Standing
	for _, x := range e.standing {
		if x.Active(now) && x.Cluster == inc.Cluster && x.Namespace == inc.Namespace && x.Workload == inc.Workload && x.Action == inc.Action.Type {
			s = x
		}
	}
	if s == nil {
		e.standSeq++
		s = &Standing{
			ID: fmt.Sprintf("SA-%04d", e.standSeq), Cluster: inc.Cluster, Namespace: inc.Namespace,
			Workload: inc.Workload, Action: inc.Action.Type, GrantedOn: inc.ID,
		}
		e.standing = append(e.standing, s)
	}
	s.GrantedBy, s.GrantedAt, s.ExpiresAt = operator, now, now.Add(ttl)
	out := *s
	e.mu.Unlock()
	e.store.Note(inc.ID, "standing-granted", operator, fmt.Sprintf("standing approval %s granted: %q may be applied to %s automatically until %s",
		out.ID, out.Action, strings.ToLower(out.Workload.String()), out.ExpiresAt.UTC().Format("2 Jan 2006")), now)
	return out
}

// matchStanding returns the active standing approval that covers the
// incident's proposed action, if policy still permits using it.
func (e *Engine) matchStanding(inc *incident.Incident, now time.Time) *Standing {
	if !e.standingAllowed(inc) {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, s := range e.standing {
		if s.Active(now) && s.Cluster == inc.Cluster && s.Namespace == inc.Namespace && s.Workload == inc.Workload && s.Action == inc.Action.Type {
			c := *s
			return &c
		}
	}
	return nil
}

func (e *Engine) markUsed(id, incidentID string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.standing {
		if s.ID == id {
			s.Uses++
			s.LastUsed, s.LastIncident = &now, incidentID
		}
	}
}

// Revoke ends a standing approval on behalf of a named operator.
func (e *Engine) Revoke(id, operator, reason string) (*Standing, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, ErrOperatorRequired
	}
	return e.revoke(id, operator, strings.TrimSpace(reason))
}

func (e *Engine) revoke(id, by, reason string) (*Standing, error) {
	now := time.Now()
	e.mu.Lock()
	var out *Standing
	for _, s := range e.standing {
		if s.ID == id && s.RevokedAt == nil {
			s.RevokedAt, s.RevokedBy, s.RevokeReason = &now, by, reason
			c := *s
			out = &c
		}
	}
	e.mu.Unlock()
	if out == nil {
		return nil, ErrStandingNotFound
	}
	note := fmt.Sprintf("standing approval %s revoked", out.ID)
	if reason != "" {
		note += ": " + reason
	}
	e.store.Note(out.GrantedOn, "standing-revoked", by, note, now)
	e.notify()
	return out, nil
}
