// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/remediate"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

type fakeGit struct {
	mu       sync.Mutex
	requests []PRRequest
	state    string
	err      error
}

func (f *fakeGit) OpenPR(_ context.Context, r PRRequest) (PRResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return PRResult{}, f.err
	}
	f.requests = append(f.requests, r)
	return PRResult{Number: 41, URL: "https://git.example/acme/shop/pull/41"}, nil
}

func (f *fakeGit) PRState(context.Context, string, int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *fakeGit) set(state string) {
	f.mu.Lock()
	f.state = state
	f.mu.Unlock()
}

// gitHarness is the OOM harness with a Git host and a Deployment that
// declares where its manifest lives.
func gitHarness(t *testing.T, git *fakeGit) (*harness, *incident.Incident) {
	t.Helper()
	h := oomHarness(t, Config{
		PullRequests: git, PublicURL: "https://kbark.example", ReplanEvery: 1,
		Planner: &remediate.Planner{MemoryCeiling: resource.MustParse("2Gi"), GitOps: true},
	})
	first := h.waitFor("a proposal", inState(incident.StateProposed))
	patch := `{"metadata":{"annotations":{"kbark.dev/git-repo":"acme/shop","kbark.dev/git-path":"apps/cart/deployment.yaml"}}}`
	if _, err := h.client.AppsV1().Deployments(tk.NS).Patch(context.Background(), "cart", types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.Replan(context.Background(), first.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	inc := h.waitFor("a proposal with a Git source", func(i *incident.Incident) bool {
		return i.State == incident.StateProposed && i.Action != nil && i.Action.Source != nil
	})
	return h, inc
}

func TestPullRequestInsteadOfALiveChange(t *testing.T) {
	git := &fakeGit{state: "open"}
	h, inc := gitHarness(t, git)
	if inc.Action.Source.Repo != "acme/shop" || inc.Action.Source.Path != "apps/cart/deployment.yaml" {
		t.Fatalf("source = %+v", inc.Action.Source)
	}
	if inc.StandingAllowed {
		t.Error("a workload with a Git source must not be offered a standing approval")
	}
	if _, err := h.engine.OpenPullRequest(context.Background(), inc.ID, " "); !errors.Is(err, ErrOperatorRequired) {
		t.Fatalf("a pull request without a name: %v", err)
	}
	got, err := h.engine.OpenPullRequest(context.Background(), inc.ID, "asha")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != incident.StatePullRequest || got.PullRequest == nil || got.PullRequest.Number != 41 {
		t.Fatalf("incident after opening = %s %+v", got.State, got.PullRequest)
	}
	if h.limit() != "64Mi" {
		t.Fatalf("opening a pull request changed the cluster: limit is %s", h.limit())
	}
	r := git.requests[0]
	if r.Kind != "Deployment" || r.Name != "cart" || r.Namespace != tk.NS || r.Action != incident.ActionSetMemoryLimit || r.Params["to"] != "128Mi" {
		t.Errorf("request = %+v", r)
	}
	if !strings.HasPrefix(r.Head, "kbark/inc-") || !strings.Contains(r.Body, "Requested by asha") || !strings.Contains(r.Body, "https://kbark.example/incidents/"+inc.ID) || !strings.Contains(r.Body, "Memory limit") {
		t.Errorf("head %q, body:\n%s", r.Head, r.Body)
	}
	// It cannot also be approved live while the pull request is open.
	if _, err := h.engine.Approve(context.Background(), inc.ID, "asha"); !errors.Is(err, incident.ErrWrongState) {
		t.Errorf("approving live with a pull request open: %v", err)
	}

	// Merged: noted once. The incident stays open until the failure
	// actually clears (the store's own test covers that transition).
	git.set("merged")
	merged := h.waitFor("the merge to be noticed", func(i *incident.Incident) bool {
		return i.ID == inc.ID && i.PullRequest != nil && i.PullRequest.State == "merged"
	})
	if merged.State != incident.StatePullRequest {
		t.Errorf("a merge alone must not close the incident, got %s", merged.State)
	}
	for _, a := range h.client.Actions() {
		if a.GetVerb() == "update" {
			t.Errorf("kbark wrote to the cluster: %s %s", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

func TestClosedPullRequestHandsTheDecisionBack(t *testing.T) {
	git := &fakeGit{state: "open"}
	h, inc := gitHarness(t, git)
	if _, err := h.engine.OpenPullRequest(context.Background(), inc.ID, "asha"); err != nil {
		t.Fatal(err)
	}
	git.set("closed")
	back := h.waitFor("the fix proposed again", func(i *incident.Incident) bool { return i.ID == inc.ID && i.State == incident.StateProposed })
	if back.Action == nil {
		t.Fatal("the proposal is gone")
	}
}

func TestPullRequestNeedsASourceAndAHost(t *testing.T) {
	h := oomHarness(t, Config{PullRequests: &fakeGit{}})
	inc := h.waitFor("a proposal", inState(incident.StateProposed))
	if inc.Action.Source != nil {
		t.Fatal("no annotations and GitOps off: there must be no source")
	}
	if _, err := h.engine.OpenPullRequest(context.Background(), inc.ID, "asha"); !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v, want ErrNoSource", err)
	}

	git := &fakeGit{err: errors.New("GitHub answered 404: Not Found")}
	h2, inc2 := gitHarness(t, git)
	if _, err := h2.engine.OpenPullRequest(context.Background(), inc2.ID, "asha"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a failed pull request must surface the host's answer, got %v", err)
	}
	if got, _ := h2.store.Get(inc2.ID); got.State != incident.StateProposed {
		t.Errorf("after a failed pull request the fix must still be proposed, got %s", got.State)
	}
}
