// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/policy"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

func standingPolicy() *policy.Policy {
	p := policy.Default(nil, "2Gi")
	p.Defaults.StandingApprovals = policy.StandingLowRisk
	p.Defaults.MaxAttempts = 5
	return p
}

// failAgain adds a fresh OOM-killed pod of the same workload, as if the
// workload had run out of memory again some time later.
func (h *harness) failAgain(suffix string) {
	h.t.Helper()
	d := tk.Deployment("cart", tk.Container("cart:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("cart:1"))
	if _, err := h.client.CoreV1().Pods(tk.NS).Create(context.Background(), tk.Pod(rs, suffix, tk.OOMKilled()), metav1.CreateOptions{}); err != nil {
		h.t.Fatal(err)
	}
}

func TestStandingApprovalIsOffByDefault(t *testing.T) {
	h := oomHarness(t, Config{})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if inc.StandingAllowed {
		t.Error("the default policy must not offer standing approvals")
	}
	if _, err := h.engine.ApproveWith(context.Background(), inc.ID, "asha", ApproveOptions{Standing: true}); !errors.Is(err, ErrStandingNotAllowed) {
		t.Fatalf("err = %v, want ErrStandingNotAllowed", err)
	}
	if got := h.limit(); got != "64Mi" {
		t.Fatalf("a refused standing approval still changed the cluster: limit is %s", got)
	}
	if len(h.engine.Standings()) != 0 {
		t.Error("a standing approval was recorded")
	}
}

func TestStandingApprovalAppliesTheNextFixAndIsRevokedOnFailure(t *testing.T) {
	h := oomHarness(t, Config{Policy: standingPolicy()})
	first := h.waitFor("a proposal", inState(incident.StateProposed))
	if !first.StandingAllowed {
		t.Fatal("policy allows standing approvals of low-risk fixes; the proposal must say so")
	}
	if _, err := h.engine.ApproveWith(context.Background(), first.ID, "asha", ApproveOptions{Standing: true}); err != nil {
		t.Fatal(err)
	}
	h.deletePods() // the fix helped
	h.waitFor("the first fix verified", func(i *incident.Incident) bool { return i.ID == first.ID && i.State == incident.StateVerified })

	st := h.engine.Standings()
	if len(st) != 1 || st[0].GrantedBy != "asha" || st[0].Action != incident.ActionSetMemoryLimit ||
		st[0].Workload != (fleet.Ref{Kind: "Deployment", Name: "cart"}) || !st[0].Active(time.Now()) {
		t.Fatalf("standing approvals = %+v", st)
	}

	// The same failure returns. Nobody clicks anything.
	h.failAgain("y")
	second := h.waitFor("the second incident applied by itself", func(i *incident.Incident) bool {
		return i.ID != first.ID && (i.State == incident.StateApplying || i.State == incident.StateVerified || i.State == incident.StateFailed)
	})
	if got := h.limit(); got != "256Mi" {
		t.Fatalf("limit after the automatic fix = %s, want 256Mi", got)
	}
	if second.StandingID != st[0].ID {
		t.Errorf("incident does not name the standing approval: %q", second.StandingID)
	}
	named := false
	for _, e := range second.History {
		if strings.Contains(e.Actor, "asha") && strings.Contains(e.Note, "standing approval "+st[0].ID) {
			named = true
		}
	}
	if !named {
		t.Errorf("the history must name the person and the standing approval: %+v", second.History)
	}

	// This time the fix does not help: the pod stays, verification
	// fails, and the trust is withdrawn.
	h.waitFor("the second fix failed", func(i *incident.Incident) bool { return i.ID == second.ID && i.State == incident.StateFailed })
	deadline := time.Now().Add(2 * time.Second)
	for h.engine.Standings()[0].RevokedAt == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := h.engine.Standings()[0]
	if got.RevokedAt == nil || got.RevokedBy != "kbark" || got.Uses != 1 || !strings.Contains(got.RevokeReason, second.ID) {
		t.Fatalf("standing approval after a failed automatic fix = %+v", got)
	}
}

func TestStandingApprovalCoversOnlyItsOwnTarget(t *testing.T) {
	h := oomHarness(t, Config{Policy: standingPolicy()})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	now := time.Now()
	other := *inc
	other.Workload = fleet.Ref{Kind: "Deployment", Name: "checkout"}
	h.engine.grant(&other, "asha", now)
	if h.engine.matchStanding(inc, now) != nil {
		t.Error("an approval for another workload must not match")
	}
	st := h.engine.grant(inc, "asha", now)
	if h.engine.matchStanding(inc, now) == nil {
		t.Fatal("an approval for this workload and action must match")
	}
	if h.engine.matchStanding(inc, st.ExpiresAt.Add(time.Second)) != nil {
		t.Error("an expired approval must not match")
	}
	medium := *inc
	a := *inc.Action
	a.Risk = "medium"
	medium.Action = &a
	if h.engine.matchStanding(&medium, now) != nil {
		t.Error("a medium-risk fix must never be applied under a standing approval")
	}
	if _, err := h.engine.Revoke(st.ID, " ", ""); !errors.Is(err, ErrOperatorRequired) {
		t.Errorf("revoking without a name: %v", err)
	}
	if _, err := h.engine.Revoke(st.ID, "vik", "no longer wanted"); err != nil {
		t.Fatal(err)
	}
	if h.engine.matchStanding(inc, now) != nil {
		t.Error("a revoked approval must not match")
	}
	if _, err := h.engine.Revoke(st.ID, "vik", ""); !errors.Is(err, ErrStandingNotFound) {
		t.Errorf("revoking twice: %v", err)
	}
}

func TestStandingApprovalsSurviveARestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	h := oomHarness(t, Config{Policy: standingPolicy(), StatePath: state})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	st := h.engine.grant(inc, "asha", time.Now())
	if err := h.engine.Save(); err != nil {
		t.Fatal(err)
	}
	e2 := New([]*fleet.Cluster{h.cluster}, incident.NewStore(nil, nil), Config{Policy: standingPolicy(), StatePath: state}, nil)
	if _, err := e2.Restore(); err != nil {
		t.Fatal(err)
	}
	got := e2.Standings()
	if len(got) != 1 || got[0].ID != st.ID || got[0].GrantedBy != "asha" {
		t.Fatalf("restored standing approvals = %+v", got)
	}
	if next := e2.grant(&incident.Incident{Cluster: "prod", Namespace: "other", Workload: inc.Workload, Action: inc.Action, ID: "INC-9"}, "asha", time.Now()); next.ID == st.ID {
		t.Error("IDs must not be reused after a restart")
	}
}
