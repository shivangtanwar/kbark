// SPDX-License-Identifier: Apache-2.0

// Package incident holds the lifecycle of detected failures: the
// incident record, the proposed fix attached to it, the in-memory
// store, and the append-only audit log.
package incident

import (
	"time"

	"github.com/shivangtanwar/kbark/internal/detect"
	"github.com/shivangtanwar/kbark/internal/fleet"
)

// State is where an incident is in its lifecycle.
type State string

const (
	// StateDetected: seen by a detector; a fix is being planned.
	StateDetected State = "detected"
	// StateProposed: a fix is waiting for a person to approve or reject it.
	StateProposed State = "proposed"
	// StateManual: no safe automatic fix exists; kbark explains and stops.
	StateManual State = "manual"
	// StateRejected: a person declined the proposed fix.
	StateRejected State = "rejected"
	// StateApplying: an approved fix has been written and is being verified.
	StateApplying State = "applying"
	// StateVerified: the fix was applied and the failure cleared.
	StateVerified State = "verified"
	// StateFailed: the fix was applied (or attempted) and the failure remains.
	StateFailed State = "failed"
	// StateRolledBack: an applied fix was undone on request.
	StateRolledBack State = "rolled-back"
	// StateResolved: the failure cleared without kbark changing anything.
	StateResolved State = "resolved"
)

// Open reports whether the incident still occupies its key, i.e. a
// fresh observation of the same failure updates it instead of opening
// a new incident.
func (s State) Open() bool {
	switch s {
	case StateVerified, StateRolledBack, StateResolved:
		return false
	}
	return true
}

// Action types: the complete write surface of kbark.
const (
	ActionSetMemoryLimit   = "set-memory-limit"
	ActionRollbackWorkload = "rollback-workload"
	ActionTuneProbe        = "tune-probe"
	ActionDeletePod        = "delete-pod"
	ActionRestartPod       = "restart-pod"
	ActionUncordonNode     = "uncordon-node"
	// ActionSetEphemeralLimit raises a container's ephemeral-storage limit.
	ActionSetEphemeralLimit = "set-ephemeral-storage-limit"
	// ActionSetPullPolicy changes a container's image pull policy from
	// Never to IfNotPresent.
	ActionSetPullPolicy = "set-image-pull-policy"
	// ActionFixServiceSelector points a Service's selector at the pods
	// whose labels it nearly matches.
	ActionFixServiceSelector = "fix-service-selector"
)

// Action is one typed, previewable change. It is data, not a command:
// nothing here is executed except by the matching code in remediate.
type Action struct {
	Type string `json:"type"`
	// Target is the object that will be written.
	TargetKind string `json:"targetKind"`
	TargetName string `json:"targetName"`
	// TargetUID and TargetGeneration pin the proposal to the exact
	// object and spec it was computed from. Apply refuses if either
	// has changed.
	TargetUID        string `json:"targetUID"`
	TargetGeneration int64  `json:"targetGeneration"`

	Params     map[string]string `json:"params,omitempty"`
	Title      string            `json:"title"`
	Rationale  string            `json:"rationale"`
	Risk       string            `json:"risk"` // low | medium
	Diff       string            `json:"diff"`
	Warnings   []string          `json:"warnings,omitempty"`
	Reversible bool              `json:"reversible"`

	// Template is the pod template a rollback restores. Internal.
	Template []byte `json:"-"`
}

// Undo is what is needed to reverse an applied action.
type Undo struct {
	// PrevTemplate is the workload's pod template before the fix.
	PrevTemplate []byte `json:"prevTemplate,omitempty"`
	// AppliedGeneration is the workload generation right after the
	// fix; rollback refuses if someone has changed the spec since.
	AppliedGeneration int64 `json:"appliedGeneration,omitempty"`
	// PrevSelector is a Service's selector before the fix.
	PrevSelector map[string]string `json:"prevSelector,omitempty"`
}

// HistoryEntry is one step in an incident's life.
type HistoryEntry struct {
	Time  time.Time `json:"time"`
	State State     `json:"state"`
	Actor string    `json:"actor"`
	Note  string    `json:"note,omitempty"`
}

// Explanation states.
const (
	ExplainPending     = "pending"
	ExplainReady       = "ready"
	ExplainUnavailable = "unavailable"
	ExplainError       = "error"
)

// Incident is one failure on one workload.
type Incident struct {
	ID        string            `json:"id"`
	Cluster   string            `json:"cluster"`
	Namespace string            `json:"namespace"`
	Workload  fleet.Ref         `json:"workload"`
	Key       string            `json:"key"`
	Family    detect.Family     `json:"family"`
	Reason    string            `json:"reason"`
	Cause     string            `json:"cause,omitempty"`
	Summary   string            `json:"summary"`
	Container string            `json:"container,omitempty"`
	Init      bool              `json:"init,omitempty"`
	Probe     string            `json:"probe,omitempty"`
	Evidence  []detect.Evidence `json:"evidence"`
	Pods      []string          `json:"pods"`

	State        State   `json:"state"`
	Action       *Action `json:"action,omitempty"`
	ManualReason string  `json:"manualReason,omitempty"`
	CanRollback  bool    `json:"canRollback"`
	Undo         *Undo   `json:"-"`

	Explanation      string `json:"explanation,omitempty"`
	ExplanationState string `json:"explanationState"`
	ExplanationNote  string `json:"explanationNote,omitempty"`
	// ExplainStarted guards against requesting the explanation twice.
	ExplainStarted bool `json:"-"`

	OpenedAt  time.Time      `json:"openedAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	ClosedAt  *time.Time     `json:"closedAt,omitempty"`
	LastSeen  time.Time      `json:"lastSeen"`
	History   []HistoryEntry `json:"history"`
}

// Finding rebuilds the detector finding the incident was opened from.
func (i *Incident) Finding() detect.Finding {
	return detect.Finding{
		Family: i.Family, Reason: i.Reason, Cause: i.Cause, Summary: i.Summary,
		Container: i.Container, Init: i.Init, Probe: i.Probe, Evidence: i.Evidence,
	}
}

func (i *Incident) clone() *Incident {
	c := *i
	c.Evidence = append([]detect.Evidence(nil), i.Evidence...)
	// Never nil: an incident without pods (a refused pod creation) must
	// still serialize as a list.
	c.Pods = append([]string{}, i.Pods...)
	c.History = append([]HistoryEntry(nil), i.History...)
	if i.Action != nil {
		a := *i.Action
		a.Warnings = append([]string(nil), i.Action.Warnings...)
		a.Params = map[string]string{}
		for k, v := range i.Action.Params {
			a.Params[k] = v
		}
		c.Action = &a
	}
	if i.ClosedAt != nil {
		t := *i.ClosedAt
		c.ClosedAt = &t
	}
	return &c
}
