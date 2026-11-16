// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/policy"
	"github.com/shivangtanwar/kbark/internal/remediate"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

func TestAppliedFixSurvivesRestartAndCanStillBeRolledBack(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	h := oomHarness(t, Config{StatePath: state, SampleEvery: time.Millisecond})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	h.deletePods()
	h.waitFor("verification", func(i *incident.Incident) bool { return i.ID == inc.ID && i.State == incident.StateVerified })
	if err := h.engine.Save(); err != nil {
		t.Fatal(err)
	}
	if len(h.engine.History(time.Time{})) == 0 {
		t.Fatal("no trend samples were recorded")
	}

	// A new process: fresh store and engine over the same cluster.
	store := incident.NewStore(nil, nil)
	e2 := New([]*fleet.Cluster{h.cluster}, store, Config{
		StatePath: state, Planner: &remediate.Planner{MemoryCeiling: resource.MustParse("2Gi")},
	}, nil)
	n, err := e2.Restore()
	if err != nil || n != 1 {
		t.Fatalf("restore: n=%d err=%v", n, err)
	}
	got, err := store.Get(inc.ID)
	if err != nil || got.State != incident.StateVerified || !got.CanRollback || got.Undo == nil {
		t.Fatalf("restored incident lost its state: %+v err=%v", got, err)
	}
	if len(e2.History(time.Time{})) == 0 {
		t.Fatal("trend samples were not restored")
	}
	if _, err := e2.Rollback(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatalf("rollback after restart: %v", err)
	}
	if got := h.limit(); got != "64Mi" {
		t.Fatalf("limit after post-restart rollback = %s, want 64Mi", got)
	}
	// New incidents continue the numbering instead of reusing IDs.
	created := store.Observe("prod", []fleet.Observation{{Key: "k", Namespace: "x", Workload: fleet.Ref{Kind: "Deployment", Name: "y"}}}, time.Now())
	if len(created) != 1 || created[0].ID == inc.ID {
		t.Fatalf("ID reuse after restore: %+v", created)
	}
}

func TestRestoreToleratesMissingFileAndRejectsCorruptOne(t *testing.T) {
	dir := t.TempDir()
	e := New(nil, incident.NewStore(nil, nil), Config{StatePath: filepath.Join(dir, "none.json")}, nil)
	if n, err := e.Restore(); n != 0 || err != nil {
		t.Fatalf("missing file: n=%d err=%v", n, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := writeFile(bad, "{not json"); err != nil {
		t.Fatal(err)
	}
	e = New(nil, incident.NewStore(nil, nil), Config{StatePath: bad}, nil)
	if _, err := e.Restore(); err == nil || !strings.Contains(err.Error(), "move it aside") {
		t.Fatalf("corrupt file should say what to do: %v", err)
	}
}

func TestPolicyCanForbidAnAction(t *testing.T) {
	pol := policy.Default(nil, "2Gi")
	pol.Rules = []policy.Rule{{Name: "no-memory-changes", Namespaces: []string{tk.NS}, Actions: []string{"rollback-workload"}}}
	h := oomHarness(t, Config{Policy: pol})
	inc := h.waitFor("a manual incident", inState(incident.StateManual))
	if inc.Action != nil || !strings.Contains(inc.ManualReason, "no-memory-changes") || !strings.Contains(inc.ManualReason, "set-memory-limit") {
		t.Fatalf("want the policy to explain itself, got %q", inc.ManualReason)
	}
}

func TestPolicyMemoryCeilingApplies(t *testing.T) {
	pol := policy.Default(nil, "2Gi")
	pol.Rules = []policy.Rule{{Name: "tight", Namespaces: []string{tk.NS}, MemoryCeiling: "100Mi"}}
	h := oomHarness(t, Config{Policy: pol})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if inc.Action.Params["to"] != "100Mi" {
		t.Fatalf("proposal should be capped at the rule's ceiling, got %s", inc.Action.Params["to"])
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }
