// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/ai"
	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/remediate"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

type fakeProvider struct {
	mu       sync.Mutex
	requests []ai.Request
	reply    string
	err      error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Stream(_ context.Context, req ai.Request) (<-chan ai.Event, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	ch := make(chan ai.Event, 2)
	if f.err != nil {
		ch <- ai.ErrorEvent{Err: f.err}
	} else {
		ch <- ai.TextDeltaEvent{Delta: f.reply}
		ch <- ai.DoneEvent{}
	}
	close(ch)
	return ch, nil
}

type harness struct {
	t       *testing.T
	engine  *Engine
	store   *incident.Store
	audit   *incident.Audit
	cluster *fleet.Cluster
	client  *fake.Clientset
}

// oomHarness is a cluster with one Deployment whose pod is OOM-killed,
// and an engine running against it with short verification windows.
func oomHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	d := tk.Deployment("cart", tk.Container("cart:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("cart:1"))
	c, client := tk.Cluster(t, "prod", d, rs, tk.Pod(rs, "x", tk.OOMKilled()))

	audit, _ := incident.OpenAudit("")
	store := incident.NewStore(audit, nil)
	cfg.Interval = 10 * time.Millisecond
	cfg.VerifyHold = 60 * time.Millisecond
	cfg.VerifyTimeout = 600 * time.Millisecond
	if cfg.Planner == nil {
		cfg.Planner = &remediate.Planner{MemoryCeiling: resource.MustParse("2Gi")}
	}
	e := New([]*fleet.Cluster{c}, store, cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	// Run saves its state as it stops; wait for that so the write
	// cannot race the removal of a test's temporary directory.
	t.Cleanup(func() { cancel(); <-stopped })
	go func() { e.Run(ctx); close(stopped) }()
	return &harness{t: t, engine: e, store: store, audit: audit, cluster: c, client: client}
}

func (h *harness) waitFor(what string, cond func(*incident.Incident) bool) *incident.Incident {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, inc := range h.store.List() {
			if cond(inc) {
				return inc
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; incidents: %+v", what, h.store.List())
	return nil
}

func inState(s incident.State) func(*incident.Incident) bool {
	return func(i *incident.Incident) bool { return i.State == s }
}

func (h *harness) limit() string {
	d, err := h.client.AppsV1().Deployments(tk.NS).Get(context.Background(), "cart", metav1.GetOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	q := d.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	return q.String()
}

func (h *harness) deletePods() {
	pods, _ := h.client.CoreV1().Pods(tk.NS).List(context.Background(), metav1.ListOptions{})
	for _, p := range pods.Items {
		_ = h.client.CoreV1().Pods(tk.NS).Delete(context.Background(), p.Name, metav1.DeleteOptions{})
	}
}

func TestNothingIsWrittenWithoutApproval(t *testing.T) {
	h := oomHarness(t, Config{})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if inc.Action == nil || inc.Action.Type != incident.ActionSetMemoryLimit {
		t.Fatalf("unexpected proposal: %+v", inc.Action)
	}
	time.Sleep(100 * time.Millisecond) // several scan intervals
	if got := h.limit(); got != "64Mi" {
		t.Fatalf("the cluster changed without approval: limit is %s", got)
	}
	for _, a := range h.client.Actions() {
		switch a.GetVerb() {
		case "get", "list", "watch":
		default:
			t.Errorf("unexpected write before approval: %s %s", a.GetVerb(), a.GetResource().Resource)
		}
	}
	if _, err := h.engine.Approve(context.Background(), inc.ID, "   "); !errors.Is(err, ErrOperatorRequired) {
		t.Fatalf("approval without a name must be refused, got %v", err)
	}
	if got := h.limit(); got != "64Mi" {
		t.Fatalf("an unsigned approval changed the cluster: limit is %s", got)
	}
}

func TestApproveVerifyRollback(t *testing.T) {
	h := oomHarness(t, Config{})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))

	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	if got := h.limit(); got != "128Mi" {
		t.Fatalf("limit after approval = %s, want 128Mi", got)
	}
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); !errors.Is(err, incident.ErrWrongState) {
		t.Fatalf("second approval must be refused, got %v", err)
	}

	h.deletePods() // the rollout replaces the failing pod
	done := h.waitFor("verification", func(i *incident.Incident) bool { return i.ID == inc.ID && i.State == incident.StateVerified })
	if !done.CanRollback || done.ClosedAt == nil {
		t.Fatalf("verified incident should be closed and reversible: %+v", done)
	}

	rolled, err := h.engine.Rollback(context.Background(), inc.ID, "asha")
	if err != nil {
		t.Fatal(err)
	}
	if rolled.State != incident.StateRolledBack || h.limit() != "64Mi" {
		t.Fatalf("rollback: state=%s limit=%s", rolled.State, h.limit())
	}

	var events []string
	for _, e := range h.audit.Entries() {
		events = append(events, e.Event+":"+e.Actor)
	}
	trail := strings.Join(events, " ")
	for _, want := range []string{"detected:kbark", "proposed:kbark", "applying:asha", "applied:asha", "verified:kbark", "rolled-back:asha"} {
		if !strings.Contains(trail, want) {
			t.Errorf("audit trail lacks %q: %s", want, trail)
		}
	}
}

func TestFixThatDoesNotHelpIsMarkedFailed(t *testing.T) {
	h := oomHarness(t, Config{})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	// The pod keeps failing: never deleted, still OOM-killed.
	failed := h.waitFor("a failed verification", func(i *incident.Incident) bool { return i.ID == inc.ID && i.State == incident.StateFailed })
	if !failed.CanRollback {
		t.Fatal("a failed fix must stay reversible")
	}
	last := failed.History[len(failed.History)-1].Note
	if !strings.Contains(last, "still observed") {
		t.Errorf("failure note should say why: %q", last)
	}
}

func TestStaleApprovalIsRefusedAndRefreshed(t *testing.T) {
	h := oomHarness(t, Config{Interval: time.Hour})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))

	live, _ := h.client.AppsV1().Deployments(tk.NS).Get(context.Background(), "cart", metav1.GetOptions{})
	live.Generation = 2
	live.Spec.Template.Spec.Containers[0].Image = "cart:2"
	if _, err := h.client.AppsV1().Deployments(tk.NS).Update(context.Background(), live, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err := h.engine.Approve(context.Background(), inc.ID, "asha")
	// The scan loop may refresh the proposal before the approval lands;
	// either way the stale proposal must not be applied as approved.
	if err != nil && !errors.Is(err, ErrRefreshed) {
		t.Fatalf("unexpected error: %v", err)
	}
	if err != nil {
		if got := h.limit(); got != "64Mi" {
			t.Fatalf("a refused approval changed the cluster: limit is %s", got)
		}
		again, _ := h.store.Get(inc.ID)
		if again.State != incident.StateProposed || again.Action.TargetGeneration != 2 {
			t.Fatalf("proposal was not refreshed: state=%s generation=%d", again.State, again.Action.TargetGeneration)
		}
	}
}

func TestProtectedNamespaceIsDetectOnly(t *testing.T) {
	h := oomHarness(t, Config{Protected: []string{tk.NS}})
	inc := h.waitFor("a manual incident", inState(incident.StateManual))
	if inc.Action != nil || !strings.Contains(inc.ManualReason, "protected") {
		t.Fatalf("want detect-only, got %+v", inc)
	}
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); !errors.Is(err, incident.ErrWrongState) {
		t.Fatalf("a manual incident cannot be approved, got %v", err)
	}
}

func TestAttemptLimitStopsRepeatedFixes(t *testing.T) {
	h := oomHarness(t, Config{MaxAttempts: 1})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("a failed verification", func(i *incident.Incident) bool { return i.ID == inc.ID && i.State == incident.StateFailed })
	if _, err := h.engine.Rollback(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	// The failure is still there, so a new incident opens, but kbark
	// has used its one attempt on this workload.
	next := h.waitFor("a follow-up incident", func(i *incident.Incident) bool { return i.ID != inc.ID && i.State == incident.StateManual })
	if !strings.Contains(next.ManualReason, "already applied") {
		t.Fatalf("want the attempt limit to explain itself, got %q", next.ManualReason)
	}
}

func TestExplanationIsAdvisoryAndRedacted(t *testing.T) {
	provider := &fakeProvider{reply: "The container exceeds its limit."}
	h := oomHarness(t, Config{Explainer: NewExplainer(provider, "m1")})
	inc := h.waitFor("an explanation", func(i *incident.Incident) bool { return i.ExplanationState == incident.ExplainReady })
	if inc.Explanation != provider.reply || inc.State != incident.StateProposed {
		t.Fatalf("unexpected incident: state=%s explanation=%q", inc.State, inc.Explanation)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 1 {
		t.Fatalf("want exactly one provider call, got %d", len(provider.requests))
	}
	req := provider.requests[0]
	if len(req.Tools) != 0 {
		t.Error("the explainer must not be given tools")
	}
	payload := req.Messages[0].Content
	for _, want := range []string{"OOMKilled", "Memory limit: 64Mi", "Proposed fix (awaiting human approval): Raise memory limit", "<cluster-data>", "+        memory: 128Mi"} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload lacks %q:\n%s", want, payload)
		}
	}
}

func TestProviderFailureDoesNotBlockTheFix(t *testing.T) {
	provider := &fakeProvider{err: errors.New("401 Unauthorized\n{\"detail\": \"...\"}")}
	h := oomHarness(t, Config{Explainer: NewExplainer(provider, "m1")})
	inc := h.waitFor("an explanation error", func(i *incident.Incident) bool { return i.ExplanationState == incident.ExplainError })
	if inc.State != incident.StateProposed || inc.Action == nil {
		t.Fatalf("the proposal must not depend on the provider: %+v", inc)
	}
	if strings.Contains(inc.ExplanationNote, "\n") {
		t.Errorf("error note should be one line: %q", inc.ExplanationNote)
	}
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatalf("approval must work without an explanation: %v", err)
	}
}

func TestPayloadRedactsSecrets(t *testing.T) {
	inc := &incident.Incident{
		Namespace: "shop", Workload: fleet.Ref{Kind: "Deployment", Name: "cart"}, Family: "crash", Reason: "CrashLoopBackOff",
		Summary: "container keeps exiting",
	}
	inc.Evidence = append(inc.Evidence, struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}{"Kubelet message", "DB_PASSWORD=hunter2-super-secret failed"})
	got := Payload(context.Background(), nil, inc)
	if strings.Contains(got, "hunter2-super-secret") {
		t.Fatalf("secret leaked into the provider payload:\n%s", got)
	}
}
