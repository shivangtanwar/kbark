// SPDX-License-Identifier: Apache-2.0

package remediate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

var ctx = context.Background()

func planner() *Planner { return &Planner{MemoryCeiling: resource.MustParse("2Gi")} }

// incidentFor scans the cluster and returns the single incident it yields.
func incidentFor(t *testing.T, c *fleet.Cluster) *incident.Incident {
	t.Helper()
	obs := c.Scan(time.Now()).Observations
	if len(obs) != 1 {
		t.Fatalf("want one observation, got %d: %+v", len(obs), obs)
	}
	created := incident.NewStore(nil, nil).Observe(c.Name, obs, time.Now())
	return created[0]
}

func memoryLimit(t *testing.T, c *fleet.Cluster, name string) string {
	t.Helper()
	d, err := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	q := d.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
	return q.String()
}

func TestMemoryFixAppliesAndRollsBack(t *testing.T) {
	d := tk.Deployment("cart", tk.Container("cart:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("cart:1"))
	c, _ := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "x", tk.OOMKilled()))
	inc := incidentFor(t, c)

	plan := planner().Plan(ctx, c, inc)
	if plan.Action == nil {
		t.Fatalf("want an action, got manual: %s", plan.Manual)
	}
	a := plan.Action
	if a.Type != incident.ActionSetMemoryLimit || a.Params["to"] != "128Mi" {
		t.Fatalf("unexpected action %s %v", a.Type, a.Params)
	}
	if !strings.Contains(a.Rationale, "request equals the limit") {
		t.Errorf("the rationale must say the request is raised too: %s", a.Rationale)
	}
	if !strings.Contains(a.Diff, "-        memory: 64Mi") || !strings.Contains(a.Diff, "+        memory: 128Mi") {
		t.Errorf("diff does not show the limit change:\n%s", a.Diff)
	}
	if got := memoryLimit(t, c, "cart"); got != "64Mi" {
		t.Fatalf("planning changed the cluster: limit is %s", got)
	}

	undo, err := Apply(ctx, c, tk.NS, a)
	if err != nil {
		t.Fatal(err)
	}
	if got := memoryLimit(t, c, "cart"); got != "128Mi" {
		t.Fatalf("limit after apply = %s, want 128Mi", got)
	}
	live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "cart", metav1.GetOptions{})
	if req := live.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory]; req.String() != "128Mi" {
		t.Errorf("request should follow the limit when they were equal, got %s", req.String())
	}

	if err := Rollback(ctx, c, tk.NS, a, undo); err != nil {
		t.Fatal(err)
	}
	if got := memoryLimit(t, c, "cart"); got != "64Mi" {
		t.Fatalf("limit after rollback = %s, want 64Mi", got)
	}
}

func TestApplyRefusesWhenTargetChanged(t *testing.T) {
	d := tk.Deployment("cart", tk.Container("cart:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("cart:1"))
	c, client := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "x", tk.OOMKilled()))
	plan := planner().Plan(ctx, c, incidentFor(t, c))

	// Someone edits the Deployment after the proposal was computed.
	live, _ := client.AppsV1().Deployments(tk.NS).Get(ctx, "cart", metav1.GetOptions{})
	live.Generation = 2
	if _, err := client.AppsV1().Deployments(tk.NS).Update(ctx, live, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(ctx, c, tk.NS, plan.Action); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("want ErrPrecondition, got %v", err)
	}
	if got := memoryLimit(t, c, "cart"); got != "64Mi" {
		t.Fatalf("a refused apply changed the cluster: limit is %s", got)
	}
}

func TestMemoryCeiling(t *testing.T) {
	ctr := tk.Container("cart:1")
	ctr.Resources.Limits[corev1.ResourceMemory] = resource.MustParse("2Gi")
	d := tk.Deployment("cart", ctr)
	rs := tk.ReplicaSet(d, "1", ctr)
	c, _ := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "x", tk.OOMKilled()))
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action != nil || !strings.Contains(plan.Manual, "ceiling") {
		t.Fatalf("want manual at the ceiling, got %+v", plan)
	}
}

func TestRollbackPlannedForFailureOnLatestRevision(t *testing.T) {
	tests := []struct {
		name       string
		prevImage  string
		status     corev1.ContainerStatus
		wantAction bool
	}{
		{"crash on latest revision", "api:1", tk.Crashing(), true},
		{"bad tag introduced by latest revision", "api:1", tk.PullFailing("api:2"), true},
		{"pull failure with unchanged image cannot be fixed by rollback", "api:2", tk.PullFailing("api:2"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tk.Deployment("api", tk.Container("api:2"))
			prevCtr := tk.Container(tt.prevImage)
			prevCtr.Env = []corev1.EnvVar{{Name: "MODE", Value: "stable"}}
			prev := tk.ReplicaSet(d, "1", prevCtr)
			cur := tk.ReplicaSet(d, "2", tk.Container("api:2"))
			c, _ := tk.Cluster(t, "a", d, prev, cur, tk.Pod(cur, "x", tt.status))

			plan := planner().Plan(ctx, c, incidentFor(t, c))
			if !tt.wantAction {
				if plan.Action != nil {
					t.Fatalf("want manual, got %s", plan.Action.Title)
				}
				return
			}
			if plan.Action == nil || plan.Action.Type != incident.ActionRollbackWorkload {
				t.Fatalf("want rollback, got %+v", plan)
			}
			if _, err := Apply(ctx, c, tk.NS, plan.Action); err != nil {
				t.Fatal(err)
			}
			live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "api", metav1.GetOptions{})
			got := live.Spec.Template.Spec.Containers[0]
			if got.Image != tt.prevImage || len(got.Env) != 1 {
				t.Errorf("template not restored to revision 1: image=%s env=%v", got.Image, got.Env)
			}
			if _, ok := live.Spec.Template.Labels["pod-template-hash"]; ok {
				t.Error("pod-template-hash label must not be copied into the Deployment template")
			}
		})
	}
}

func TestNoRollbackWithoutEarlierRevision(t *testing.T) {
	d := tk.Deployment("api", tk.Container("api:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("api:1"))
	c, _ := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "x", tk.Crashing()))
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action != nil || plan.Manual == "" {
		t.Fatalf("want manual, got %+v", plan)
	}
}

func TestBarePodIsManual(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "lonely", Namespace: tk.NS, UID: "p1", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{tk.Container("x:1")}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{withName(tk.OOMKilled())}},
	}
	c, _ := tk.Cluster(t, "a", pod)
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action != nil || !strings.Contains(plan.Manual, "not managed by a controller") {
		t.Fatalf("want manual for bare pod, got %+v", plan)
	}
}

func withName(cs corev1.ContainerStatus) corev1.ContainerStatus { cs.Name = "app"; return cs }

func TestUncordon(t *testing.T) {
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-1", Namespace: tk.NS, UID: "p1", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute))},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{tk.Container("x:1")}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
			Message: "0/1 nodes are available: 1 node(s) were unschedulable.",
		}}},
	}
	c, client := tk.Cluster(t, "a", pending, tk.Node("n1", true))
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionUncordonNode || plan.Action.TargetName != "n1" {
		t.Fatalf("want uncordon n1, got %+v", plan)
	}
	undo, err := Apply(ctx, c, "", plan.Action)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := client.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{}); n.Spec.Unschedulable {
		t.Fatal("node is still cordoned")
	}
	// A second apply finds the node already schedulable and refuses.
	if _, err := Apply(ctx, c, "", plan.Action); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("want ErrPrecondition on re-apply, got %v", err)
	}
	if err := Rollback(ctx, c, "", plan.Action, undo); err != nil {
		t.Fatal(err)
	}
	if n, _ := client.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{}); !n.Spec.Unschedulable {
		t.Fatal("rollback did not cordon the node again")
	}
}

func TestDeleteEvictedPods(t *testing.T) {
	evicted := func(name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: tk.NS, UID: types.UID("uid-" + name)},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{tk.Container("x:1")}},
			Status:     corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "The node was low on resource: memory."},
		}
	}
	healthy := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cache-ok", Namespace: tk.NS, UID: "ok"},
		Spec:       corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{tk.Container("x:1")}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
	}
	c, client := tk.Cluster(t, "a", evicted("cache-dead1"), healthy)
	inc := incidentFor(t, c)
	plan := planner().Plan(ctx, c, inc)
	if plan.Action == nil || plan.Action.Type != incident.ActionDeletePod || plan.Action.Reversible {
		t.Fatalf("want irreversible delete-pod, got %+v", plan)
	}
	if undo, err := Apply(ctx, c, tk.NS, plan.Action); err != nil || undo != nil {
		t.Fatalf("apply: undo=%v err=%v", undo, err)
	}
	pods, _ := client.CoreV1().Pods(tk.NS).List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 1 || pods.Items[0].Name != "cache-ok" {
		t.Fatalf("want only the healthy pod left, got %d pods", len(pods.Items))
	}
}

func TestProbeTuning(t *testing.T) {
	d := tk.Deployment("slow", tk.Container("slow:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("slow:1"))
	pod := tk.Pod(rs, "x", corev1.ContainerStatus{
		RestartCount: 3,
		State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "Error", ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Second)),
		}},
	})
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: tk.NS},
		InvolvedObject: corev1.ObjectReference{UID: pod.UID, FieldPath: "spec.containers{app}"},
		Reason:         "Unhealthy", Message: "Liveness probe failed: dial tcp 10.0.0.1:8080: connect: connection refused",
		Count: 6, LastTimestamp: metav1.Now(),
	}
	c, _ := tk.Cluster(t, "a", runtime.Object(d), rs, pod, ev)
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionTuneProbe {
		t.Fatalf("want tune-probe, got %+v", plan)
	}
	if _, err := Apply(ctx, c, tk.NS, plan.Action); err != nil {
		t.Fatal(err)
	}
	live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "slow", metav1.GetOptions{})
	p := live.Spec.Template.Spec.Containers[0].LivenessProbe
	if p.InitialDelaySeconds != 30 || p.TimeoutSeconds != 5 {
		t.Errorf("probe = delay %d timeout %d, want 30 and 5", p.InitialDelaySeconds, p.TimeoutSeconds)
	}
	if hold := HoldFor(plan.Action, 15*time.Second); hold != 60*time.Second {
		t.Errorf("hold = %s, want 60s so the relaxed probe has run before the fix counts", hold)
	}
}

func TestManagedWorkloadCarriesWarning(t *testing.T) {
	d := tk.Deployment("cart", tk.Container("cart:1"))
	d.Labels = map[string]string{"app.kubernetes.io/managed-by": "Helm"}
	rs := tk.ReplicaSet(d, "1", tk.Container("cart:1"))
	c, _ := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "x", tk.OOMKilled()))
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || len(plan.Action.Warnings) == 0 || !strings.Contains(plan.Action.Warnings[0], "Helm") {
		t.Fatalf("want a Helm drift warning, got %+v", plan.Action)
	}
}
