// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"testing"
	"time"

	"github.com/shivangtanwar/kbark/internal/detect"
	"github.com/shivangtanwar/kbark/internal/fleet"
)

func obs(name string, family detect.Family) fleet.Observation {
	ref := fleet.Ref{Kind: "Deployment", Name: name}
	return fleet.Observation{
		Key: fleet.Key("ns", ref, family), Namespace: "ns", Workload: ref, Pods: []string{name + "-1"},
		Finding: detect.Finding{Family: family, Reason: "r", Summary: "s", Evidence: []detect.Evidence{{Label: "a", Value: "b"}}},
	}
}

func TestObserveOpensOncePerFailure(t *testing.T) {
	s := NewStore(nil, nil)
	t0 := time.Now()
	if n := len(s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0)); n != 1 {
		t.Fatalf("first scan opened %d incidents, want 1", n)
	}
	if n := len(s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0.Add(time.Second))); n != 0 {
		t.Fatalf("repeat scan opened %d incidents, want 0", n)
	}
	// Same workload name in another cluster, and another family on the
	// same workload, are separate incidents.
	if n := len(s.Observe("c2", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0)); n != 1 {
		t.Fatalf("other cluster opened %d, want 1", n)
	}
	if n := len(s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash), obs("api", detect.FamilyProbe)}, t0)); n != 1 {
		t.Fatalf("other family opened %d, want 1", n)
	}
	if got := len(s.List()); got != 3 {
		t.Fatalf("store has %d incidents, want 3", got)
	}
}

func TestResolveWaitsOutRestartFlapping(t *testing.T) {
	s := NewStore(nil, nil)
	t0 := time.Now()
	inc := s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0)[0]

	// The pod looks healthy for a few seconds between crashes.
	s.Observe("c1", nil, t0.Add(10*time.Second))
	if got, _ := s.Get(inc.ID); got.State != StateDetected {
		t.Fatalf("incident closed during a restart gap: %s", got.State)
	}
	// It fails again: same incident, no new one.
	if n := len(s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0.Add(20*time.Second))); n != 0 {
		t.Fatalf("flap opened %d new incidents", n)
	}
	// Gone for the full window: resolved, and its key is free again.
	s.Observe("c1", nil, t0.Add(20*time.Second+ResolveAfter))
	got, _ := s.Get(inc.ID)
	if got.State != StateResolved || got.ClosedAt == nil {
		t.Fatalf("want resolved and closed, got %s", got.State)
	}
	if n := len(s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0.Add(5*time.Minute))); n != 1 {
		t.Fatalf("a recurrence should open a new incident, opened %d", n)
	}
}

func TestApplyingIncidentIsNotResolvedOrRewritten(t *testing.T) {
	s := NewStore(nil, nil)
	t0 := time.Now()
	inc := s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0)[0]
	_, _ = s.Update(inc.ID, t0, func(*Incident) (State, string, string, error) { return StateApplying, "asha", "approved", nil })

	changed := obs("api", detect.FamilyCrash)
	changed.Finding.Summary = "different"
	s.Observe("c1", []fleet.Observation{changed}, t0.Add(time.Second))
	s.Observe("c1", nil, t0.Add(time.Hour))
	got, _ := s.Get(inc.ID)
	if got.State != StateApplying || got.Summary != "s" {
		t.Fatalf("an incident under verification must be left to the verifier: state=%s summary=%q", got.State, got.Summary)
	}
}

func TestAttemptsCountsAppliedFixesOnly(t *testing.T) {
	s := NewStore(nil, nil)
	t0 := time.Now()
	inc := s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0)[0]
	ref := fleet.Ref{Kind: "Deployment", Name: "api"}
	_, _ = s.Update(inc.ID, t0, func(*Incident) (State, string, string, error) { return StateApplying, "asha", "approved: x", nil })
	if n := s.Attempts("c1", "ns", ref, t0.Add(-time.Hour)); n != 0 {
		t.Fatalf("an approval that has not been applied counted as an attempt: %d", n)
	}
	s.Note(inc.ID, "applied", "asha", AppliedPrefix+"x", t0)
	if n := s.Attempts("c1", "ns", ref, t0.Add(-time.Hour)); n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	if n := s.Attempts("c1", "ns", ref, t0.Add(time.Minute)); n != 0 {
		t.Fatalf("attempts outside the window = %d, want 0", n)
	}
}

func TestAuditRecordsDiffOnProposalAndApproval(t *testing.T) {
	audit, _ := OpenAudit(t.TempDir() + "/sub/audit.jsonl")
	defer audit.Close()
	s := NewStore(audit, nil)
	t0 := time.Now()
	inc := s.Observe("c1", []fleet.Observation{obs("api", detect.FamilyCrash)}, t0)[0]
	_, _ = s.Update(inc.ID, t0, func(i *Incident) (State, string, string, error) {
		i.Action = &Action{Type: ActionRollbackWorkload, TargetKind: "Deployment", TargetName: "api", Diff: "-a\n+b\n", Title: "t"}
		return StateProposed, "kbark", "t", nil
	})
	entries := audit.Entries()
	if len(entries) != 2 || entries[0].Event != "proposed" || entries[0].Diff == "" || entries[1].Event != "detected" {
		t.Fatalf("unexpected audit trail: %+v", entries)
	}
	if audit.Err() != "" {
		t.Fatalf("audit write failed: %s", audit.Err())
	}
}
