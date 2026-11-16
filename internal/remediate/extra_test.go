// SPDX-License-Identifier: Apache-2.0

package remediate

import (
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

func yes() *bool { b := true; return &b }

func statefulSet(name string, ctr corev1.Container) *appsv1.StatefulSet {
	one := int32(1)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: tk.NS, UID: types.UID("sts-" + name), Generation: 1},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{ctr}},
			},
		},
	}
}

// controllerRevision stores a pod template the way the StatefulSet
// controller does: as a replace-patch of spec.template.
func controllerRevision(t *testing.T, owner *appsv1.StatefulSet, rev int64, ctr corev1.Container) *appsv1.ControllerRevision {
	t.Helper()
	tmpl := map[string]any{
		"$patch":   "replace",
		"metadata": map[string]any{"labels": map[string]string{"app": owner.Name}},
		"spec":     map[string]any{"containers": []corev1.Container{ctr}},
	}
	raw, err := json.Marshal(map[string]any{"spec": map[string]any{"template": tmpl}})
	if err != nil {
		t.Fatal(err)
	}
	return &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: owner.Name + "-rev" + string(rune('0'+rev)), Namespace: tk.NS,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: owner.Name, UID: owner.UID, Controller: yes()}},
		},
		Revision: rev,
		Data:     runtime.RawExtension{Raw: raw},
	}
}

func TestStatefulSetRollbackUsesControllerRevisions(t *testing.T) {
	sts := statefulSet("db", tk.Container("db:2"))
	rev1 := controllerRevision(t, sts, 1, tk.Container("db:1"))
	rev2 := controllerRevision(t, sts, 2, tk.Container("db:2"))
	st := tk.Crashing()
	st.Name = "app"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "db-0", Namespace: tk.NS, UID: "pod-db-0", CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
			Labels:          map[string]string{"app": "db", appsv1.ControllerRevisionHashLabelKey: rev2.Name},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "db", UID: sts.UID, Controller: yes()}},
		},
		Spec:   corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{tk.Container("db:2")}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{st}},
	}
	c, _ := tk.Cluster(t, "a", sts, rev1, rev2, pod)

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionRollbackWorkload || plan.Action.TargetKind != "StatefulSet" {
		t.Fatalf("want a StatefulSet rollback, got %+v", plan)
	}
	if _, err := Apply(ctx, c, tk.NS, plan.Action); err != nil {
		t.Fatal(err)
	}
	live, _ := c.Client.AppsV1().StatefulSets(tk.NS).Get(ctx, "db", metav1.GetOptions{})
	if img := live.Spec.Template.Spec.Containers[0].Image; img != "db:1" {
		t.Fatalf("image after rollback = %s, want db:1", img)
	}
}

func readyFor(pod *corev1.Pod, d time.Duration) *corev1.Pod {
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-d))}}
	return pod
}

func TestOneBadPodAmongHealthySiblingsIsReplacedNotRolledBack(t *testing.T) {
	d := tk.Deployment("api", tk.Container("api:2"))
	prev := tk.ReplicaSet(d, "1", tk.Container("api:1"))
	cur := tk.ReplicaSet(d, "2", tk.Container("api:2"))
	okStatus := corev1.ContainerStatus{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
	c, client := tk.Cluster(t, "a", d, prev, cur,
		tk.Pod(cur, "bad", tk.Crashing()),
		readyFor(tk.Pod(cur, "good1", okStatus), time.Hour),
		readyFor(tk.Pod(cur, "good2", okStatus), time.Hour))

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionRestartPod {
		t.Fatalf("want restart-pod, got %+v", plan)
	}
	if plan.Action.Reversible || plan.Action.Params["healthySiblings"] != "2" {
		t.Errorf("unexpected action: %+v", plan.Action)
	}
	if _, err := Apply(ctx, c, tk.NS, plan.Action); err != nil {
		t.Fatal(err)
	}
	pods, _ := client.CoreV1().Pods(tk.NS).List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 2 {
		t.Fatalf("want the two healthy pods left, got %d", len(pods.Items))
	}
	for _, p := range pods.Items {
		if p.Name == cur.Name+"-bad" {
			t.Fatal("the unhealthy pod was not deleted")
		}
	}
}

func TestRecentlyStartedSiblingDoesNotProveTheRevision(t *testing.T) {
	d := tk.Deployment("api", tk.Container("api:2"))
	prev := tk.ReplicaSet(d, "1", tk.Container("api:1"))
	cur := tk.ReplicaSet(d, "2", tk.Container("api:2"))
	okStatus := corev1.ContainerStatus{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
	c, _ := tk.Cluster(t, "a", d, prev, cur,
		tk.Pod(cur, "bad", tk.Crashing()),
		readyFor(tk.Pod(cur, "new", okStatus), 20*time.Second))

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionRollbackWorkload {
		t.Fatalf("a sibling ready for 20s proves nothing; want rollback, got %+v", plan)
	}
}

func TestParseOOMEvents(t *testing.T) {
	metrics := `# HELP container_oom_events_total Count of out of memory events observed for the container
# TYPE container_oom_events_total counter
container_oom_events_total{container="",id="/",image="",name="",namespace="",pod=""} 0 1794783539244
container_oom_events_total{container="app",id="/kubepods/x",image="img",name="c1",namespace="shop",pod="cart-1"} 3 1794783539244
container_oom_events_total{container="app",id="/kubepods/y",image="img",name="c2",namespace="shop",pod="cart-2"} 9 1794783539244
container_oom_events_total{container="sidecar",id="/kubepods/z",image="img",name="c3",namespace="shop",pod="cart-1"} 5 1794783539244
`
	if got := fleet.ParseOOMEvents(metrics, "shop", "cart-1", "app"); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
	if got := fleet.ParseOOMEvents(metrics, "shop", "cart-9", "app"); got != 0 {
		t.Errorf("unknown pod: got %d, want 0", got)
	}
}
