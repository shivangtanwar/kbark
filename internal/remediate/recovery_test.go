// SPDX-License-Identifier: Apache-2.0

package remediate

import (
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shivangtanwar/kbark/internal/detect"
	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

// eventually polls until the informer cache shows what a write changed.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEphemeralStorageFixThenDeadRecords(t *testing.T) {
	ctr := tk.Container("cache:1")
	ctr.Resources.Limits[corev1.ResourceEphemeralStorage] = resource.MustParse("16Mi")
	d := tk.Deployment("cache", ctr)
	rs := tk.ReplicaSet(d, "1", ctr)
	dead := tk.Pod(rs, "dead", corev1.ContainerStatus{})
	dead.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "Pod ephemeral local storage usage exceeds the total limit of containers 16Mi. "}
	c, _ := tk.Cluster(t, "a", d, rs, dead)

	inc := incidentFor(t, c)
	if inc.Family != detect.FamilyDisk {
		t.Fatalf("family = %s, want disk", inc.Family)
	}
	plan := planner().Plan(ctx, c, inc)
	if plan.Action == nil || plan.Action.Type != incident.ActionSetEphemeralLimit || plan.Action.Params["to"] != "32Mi" {
		t.Fatalf("want the limit doubled to 32Mi, got %+v", plan)
	}
	undo, err := Apply(ctx, c, tk.NS, plan.Action)
	if err != nil || undo == nil {
		t.Fatalf("apply: undo=%v err=%v", undo, err)
	}

	// With the limit raised, the old pod is only a dead record, and the
	// playbook for that is to delete it.
	eventually(t, "the eviction to be reclassified", func() bool {
		obs := c.Scan(time.Now()).Observations
		return len(obs) == 1 && obs[0].Finding.Family == detect.FamilyEvicted
	})
	next := planner().Plan(ctx, c, incidentFor(t, c))
	if next.Action == nil || next.Action.Type != incident.ActionDeletePod {
		t.Fatalf("want delete-pod for the leftover record, got %+v", next)
	}

	if err := Rollback(ctx, c, tk.NS, plan.Action, undo); err != nil {
		t.Fatal(err)
	}
	live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "cache", metav1.GetOptions{})
	if q := live.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceEphemeralStorage]; q.String() != "16Mi" {
		t.Errorf("limit after rollback = %s, want 16Mi", q.String())
	}
}

func TestEphemeralStorageCeiling(t *testing.T) {
	ctr := tk.Container("cache:1")
	ctr.Resources.Limits[corev1.ResourceEphemeralStorage] = resource.MustParse("2Gi")
	d := tk.Deployment("cache", ctr)
	rs := tk.ReplicaSet(d, "1", ctr)
	dead := tk.Pod(rs, "dead", corev1.ContainerStatus{})
	dead.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: `Container app exceeded its local ephemeral storage limit "2Gi". `}
	c, _ := tk.Cluster(t, "a", d, rs, dead)
	if plan := planner().Plan(ctx, c, incidentFor(t, c)); plan.Action != nil || !strings.Contains(plan.Manual, "ceiling") {
		t.Fatalf("want manual at the ceiling, got %+v", plan)
	}
}

func TestPullPolicyFix(t *testing.T) {
	ctr := tk.Container("tool:1")
	ctr.ImagePullPolicy = corev1.PullNever
	d := tk.Deployment("tool", ctr)
	rs := tk.ReplicaSet(d, "1", ctr)
	pod := tk.Pod(rs, "x", corev1.ContainerStatus{
		Image: "tool:1",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImageNeverPull", Message: `Container image "tool:1" is not present with pull policy of Never`}},
	})
	pod.Status.Phase = corev1.PodPending
	c, _ := tk.Cluster(t, "a", d, rs, pod)

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionSetPullPolicy || !plan.Action.Reversible {
		t.Fatalf("want set-image-pull-policy, got %+v", plan)
	}
	if !strings.Contains(plan.Action.Diff, "IfNotPresent") {
		t.Errorf("diff does not show the new policy:\n%s", plan.Action.Diff)
	}
	policy := func() corev1.PullPolicy {
		live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "tool", metav1.GetOptions{})
		return live.Spec.Template.Spec.Containers[0].ImagePullPolicy
	}
	undo, err := Apply(ctx, c, tk.NS, plan.Action)
	if err != nil || policy() != corev1.PullIfNotPresent {
		t.Fatalf("after apply: policy=%s err=%v", policy(), err)
	}
	if err := Rollback(ctx, c, tk.NS, plan.Action, undo); err != nil || policy() != corev1.PullNever {
		t.Fatalf("after rollback: policy=%s err=%v", policy(), err)
	}
}

func service(name string, sel map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: tk.NS, UID: types.UID("svc-" + name)},
		Spec:       corev1.ServiceSpec{Selector: sel},
	}
}

func running() corev1.ContainerStatus {
	return corev1.ContainerStatus{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
}

func TestServiceSelectorFix(t *testing.T) {
	d := tk.Deployment("checkout", tk.Container("c:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("c:1"))
	c, client := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "a", running()), tk.Pod(rs, "b", running()),
		service("checkout", map[string]string{"app": "check-out"}))
	selector := func() string {
		s, _ := client.CoreV1().Services(tk.NS).Get(ctx, "checkout", metav1.GetOptions{})
		return s.Spec.Selector["app"]
	}

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	a := plan.Action
	if a == nil || a.Type != incident.ActionFixServiceSelector || a.TargetKind != "Service" || !a.Reversible {
		t.Fatalf("want fix-service-selector, got %+v", plan)
	}
	if !strings.Contains(a.Diff, "-  app: check-out") || !strings.Contains(a.Diff, "+  app: checkout") {
		t.Errorf("diff does not show the selector change:\n%s", a.Diff)
	}
	if Stale(c, tk.NS, a) {
		t.Error("a fresh proposal must not be stale")
	}
	undo, err := Apply(ctx, c, tk.NS, a)
	if err != nil || selector() != "checkout" {
		t.Fatalf("after apply: selector=%q err=%v", selector(), err)
	}
	eventually(t, "the Service to select its pods", func() bool { return len(c.Scan(time.Now()).Observations) == 0 })
	if !Stale(c, tk.NS, a) {
		t.Error("once applied, the same proposal must be stale")
	}
	// Approving the same proposal twice must not write again.
	if _, err := Apply(ctx, c, tk.NS, a); !errors.Is(err, ErrPrecondition) {
		t.Errorf("second apply: err = %v, want ErrPrecondition", err)
	}
	if err := Rollback(ctx, c, tk.NS, a, undo); err != nil || selector() != "check-out" {
		t.Fatalf("after rollback: selector=%q err=%v", selector(), err)
	}
}

func TestServiceSelectorAmbiguousIsManual(t *testing.T) {
	one := tk.Deployment("checkout", tk.Container("c:1"))
	rs1 := tk.ReplicaSet(one, "1", tk.Container("c:1"))
	two := tk.Deployment("check.out", tk.Container("c:1"))
	rs2 := tk.ReplicaSet(two, "1", tk.Container("c:1"))
	c, _ := tk.Cluster(t, "a", one, rs1, two, rs2, tk.Pod(rs1, "a", running()), tk.Pod(rs2, "a", running()),
		service("checkout", map[string]string{"app": "check-out"}))
	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action != nil || !strings.Contains(plan.Manual, "more than one workload") {
		t.Fatalf("two candidate workloads must not be guessed between, got %+v", plan)
	}
}

func TestReadinessTimeoutIsTuned(t *testing.T) {
	ctr := tk.Container("api:1")
	ctr.ReadinessProbe = &corev1.Probe{
		ProbeHandler:   corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"check"}}},
		TimeoutSeconds: 1,
	}
	d := tk.Deployment("api", ctr)
	rs := tk.ReplicaSet(d, "1", ctr)
	pod := tk.Pod(rs, "x", corev1.ContainerStatus{
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Now().Add(-3 * time.Minute))}},
	})
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: tk.NS},
		InvolvedObject: corev1.ObjectReference{UID: pod.UID, FieldPath: "spec.containers{app}"},
		Reason:         "Unhealthy", Message: `Readiness probe failed: command timed out: "check" timed out after 1s`,
		Count: 20, LastTimestamp: metav1.Now(),
	}
	c, _ := tk.Cluster(t, "a", d, rs, pod, ev)

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	if plan.Action == nil || plan.Action.Type != incident.ActionTuneProbe || plan.Action.Params["probe"] != "readiness" {
		t.Fatalf("want a readiness tune-probe, got %+v", plan)
	}
	if _, err := Apply(ctx, c, tk.NS, plan.Action); err != nil {
		t.Fatal(err)
	}
	live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "api", metav1.GetOptions{})
	p := live.Spec.Template.Spec.Containers[0].ReadinessProbe
	if p.TimeoutSeconds != 5 || p.InitialDelaySeconds != 0 {
		t.Errorf("readiness probe = timeout %d delay %d, want 5 and an unchanged 0", p.TimeoutSeconds, p.InitialDelaySeconds)
	}
}

func TestReadinessFailureAnswerIsNotTuned(t *testing.T) {
	ctr := tk.Container("api:1")
	ctr.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/ready"}}}
	d := tk.Deployment("api", ctr)
	rs := tk.ReplicaSet(d, "1", ctr)
	pod := tk.Pod(rs, "x", corev1.ContainerStatus{
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Now().Add(-3 * time.Minute))}},
	})
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: tk.NS},
		InvolvedObject: corev1.ObjectReference{UID: pod.UID, FieldPath: "spec.containers{app}"},
		Reason:         "Unhealthy", Message: "Readiness probe failed: HTTP probe failed with statuscode: 503",
		Count: 20, LastTimestamp: metav1.Now(),
	}
	c, _ := tk.Cluster(t, "a", d, rs, pod, ev)
	if plan := planner().Plan(ctx, c, incidentFor(t, c)); plan.Action != nil {
		t.Fatalf("an application that answers 503 must not have its probe relaxed, got %+v", plan.Action)
	}
}

// refused builds a Deployment whose latest revision cannot create pods:
// revision 1 runs one ready pod, revision 2 has none and a FailedCreate
// event with the given message.
func refused(t *testing.T, message string, mutate func(*corev1.PodSpec)) (*fleet.Cluster, *incident.Incident) {
	t.Helper()
	one := int32(1)
	d := tk.Deployment("api", tk.Container("api:2"))
	mutate(&d.Spec.Template.Spec)
	prev := tk.ReplicaSet(d, "1", tk.Container("api:1"))
	prev.Spec.Replicas = &one
	prev.Status = appsv1.ReplicaSetStatus{Replicas: 1, ReadyReplicas: 1}
	cur := tk.ReplicaSet(d, "2", tk.Container("api:2"))
	cur.Spec.Replicas = &one
	mutate(&cur.Spec.Template.Spec)
	ev := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "ev1", Namespace: tk.NS},
		InvolvedObject: corev1.ObjectReference{Kind: "ReplicaSet", Name: cur.Name, UID: cur.UID},
		Reason:         "FailedCreate", Message: message, Count: 14, LastTimestamp: metav1.Now(),
	}
	c, _ := tk.Cluster(t, "a", d, prev, cur, tk.Pod(prev, "ok", running()), ev)
	inc := incidentFor(t, c)
	if inc.Family != detect.FamilyAdmission || inc.Workload != (fleet.Ref{Kind: "Deployment", Name: "api"}) {
		t.Fatalf("want an admission incident on the Deployment, got %s on %s", inc.Family, inc.Workload)
	}
	return c, inc
}

func TestRefusedPodCreationRollsBack(t *testing.T) {
	c, inc := refused(t, `Error creating: pods "api-rev2-" is forbidden: error looking up service account shop/ghost: serviceaccount "ghost" not found`,
		func(s *corev1.PodSpec) { s.ServiceAccountName = "ghost" })
	plan := planner().Plan(ctx, c, inc)
	if plan.Action == nil || plan.Action.Type != incident.ActionRollbackWorkload {
		t.Fatalf("want a rollback to the revision that was accepted, got %+v", plan)
	}
	if _, err := Apply(ctx, c, tk.NS, plan.Action); err != nil {
		t.Fatal(err)
	}
	live, _ := c.Client.AppsV1().Deployments(tk.NS).Get(ctx, "api", metav1.GetOptions{})
	if sa := live.Spec.Template.Spec.ServiceAccountName; sa != "" {
		t.Errorf("serviceAccountName after rollback = %q, want it gone", sa)
	}
}

func TestQuotaRefusalIsManual(t *testing.T) {
	c, inc := refused(t, `Error creating: pods "api-rev2-" is forbidden: exceeded quota: compute, requested: pods=1, used: pods=4, limited: pods=4`,
		func(*corev1.PodSpec) {})
	if plan := planner().Plan(ctx, c, inc); plan.Action != nil || !strings.Contains(plan.Manual, "ResourceQuota") {
		t.Fatalf("a full quota is a capacity decision, got %+v", plan)
	}
}

func TestDaemonSetRollbackUsesControllerRevisions(t *testing.T) {
	yes := true
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: tk.NS, UID: "ds-agent", Generation: 2},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "agent"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "agent"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{tk.Container("agent:2")}},
			},
		},
	}
	owner := []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: ds.Name, UID: ds.UID, Controller: &yes}}
	revision := func(n int64, hash, image string) *appsv1.ControllerRevision {
		// The same storage format as a StatefulSet's revisions.
		cr := controllerRevision(t, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: ds.Name}}, n, tk.Container(image))
		cr.Name, cr.OwnerReferences = ds.Name+"-"+hash, owner
		return cr
	}
	st := tk.Crashing()
	st.Name = "app"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "agent-x7k2p", Namespace: tk.NS, UID: "pod-agent", CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
			// DaemonSet pods carry only the hash part of the revision name.
			Labels:          map[string]string{"app": "agent", appsv1.ControllerRevisionHashLabelKey: "bbb222"},
			OwnerReferences: owner,
		},
		Spec:   corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{tk.Container("agent:2")}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{st}},
	}
	c, client := tk.Cluster(t, "a", ds, revision(1, "aaa111", "agent:1"), revision(2, "bbb222", "agent:2"), pod)

	plan := planner().Plan(ctx, c, incidentFor(t, c))
	a := plan.Action
	if a == nil || a.Type != incident.ActionRollbackWorkload || a.TargetKind != "DaemonSet" {
		t.Fatalf("want a DaemonSet rollback, got %+v", plan)
	}
	if a.Params["pods"] != "" {
		t.Error("a DaemonSet replaces its own pods; the fix must not delete any")
	}
	if _, err := Apply(ctx, c, tk.NS, a); err != nil {
		t.Fatal(err)
	}
	live, _ := client.AppsV1().DaemonSets(tk.NS).Get(ctx, "agent", metav1.GetOptions{})
	if img := live.Spec.Template.Spec.Containers[0].Image; img != "agent:1" {
		t.Fatalf("image after rollback = %s, want agent:1", img)
	}
}

func TestDiskFixIsHeldLongEnoughToRefill(t *testing.T) {
	if got := HoldFor(&incident.Action{Type: incident.ActionSetEphemeralLimit}, 15*time.Second); got != time.Minute {
		t.Errorf("hold = %s, want 1m", got)
	}
}

func downNode(name string) *corev1.Node {
	n := tk.Node(name, false)
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionUnknown, LastTransitionTime: metav1.NewTime(time.Now().Add(-10 * time.Minute))}}
	return n
}

// stuck returns a pod of rs that was deleted ten minutes ago on node.
func stuck(rs *appsv1.ReplicaSet, node string, finalizers ...string) *corev1.Pod {
	p := tk.Pod(rs, "gone", running())
	d := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	p.DeletionTimestamp, p.Finalizers, p.Spec.NodeName = &d, finalizers, node
	return p
}

// terminatingIncident returns the pod-stuck-terminating incident among
// whatever else the scan reports (a down node is its own incident).
func terminatingIncident(t *testing.T, c *fleet.Cluster) *incident.Incident {
	t.Helper()
	for _, inc := range incident.NewStore(nil, nil).Observe(c.Name, c.Scan(time.Now()).Observations, time.Now()) {
		if inc.Family == detect.FamilyTerminating {
			return inc
		}
	}
	t.Fatal("no terminating incident")
	return nil
}

func TestForceDeleteWhenTheNodeIsDown(t *testing.T) {
	d := tk.Deployment("api", tk.Container("api:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("api:1"))
	pod := stuck(rs, "n1")
	c, client := tk.Cluster(t, "a", d, rs, pod, downNode("n1"))

	inc := terminatingIncident(t, c)
	if inc.Cause != detect.CauseNodeDown {
		t.Fatalf("cause = %q, want node-down", inc.Cause)
	}
	plan := planner().Plan(ctx, c, inc)
	a := plan.Action
	if a == nil || a.Type != incident.ActionForceDeletePod || a.Reversible || a.Risk != "medium" || len(a.Warnings) == 0 {
		t.Fatalf("want a medium-risk, irreversible force-delete with a warning, got %+v", plan)
	}
	if _, err := Apply(ctx, c, tk.NS, a); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods(tk.NS).Get(ctx, pod.Name, metav1.GetOptions{}); err == nil {
		t.Error("the stuck pod record is still there")
	}
}

func TestStuckPodsThatMustNotBeForced(t *testing.T) {
	d := tk.Deployment("api", tk.Container("api:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("api:1"))

	t.Run("node is alive", func(t *testing.T) {
		c, _ := tk.Cluster(t, "a", d, rs, stuck(rs, "n1"), tk.Node("n1", false))
		if plan := planner().Plan(ctx, c, terminatingIncident(t, c)); plan.Action != nil || !strings.Contains(plan.Manual, "kubelet is still trying") {
			t.Fatalf("a pod on a live node must not be forced, got %+v", plan)
		}
	})
	t.Run("held by a finalizer", func(t *testing.T) {
		c, _ := tk.Cluster(t, "a", d, rs, stuck(rs, "n1", "example.com/cleanup"), downNode("n1"))
		inc := terminatingIncident(t, c)
		if plan := planner().Plan(ctx, c, inc); inc.Cause != detect.CauseFinalizer || plan.Action != nil {
			t.Fatalf("a finalizer must not be bypassed: cause %q, plan %+v", inc.Cause, plan)
		}
	})
	t.Run("statefulset", func(t *testing.T) {
		sts := statefulSet("db", tk.Container("db:1"))
		yes := true
		pod := stuck(rs, "n1")
		pod.Name, pod.OwnerReferences = "db-0", []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "db", UID: sts.UID, Controller: &yes}}
		c, _ := tk.Cluster(t, "a", sts, pod, downNode("n1"))
		if plan := planner().Plan(ctx, c, terminatingIncident(t, c)); plan.Action != nil || !strings.Contains(plan.Manual, "same identity") {
			t.Fatalf("a StatefulSet pod must not be forced, got %+v", plan)
		}
	})
}

func TestNodeAndJobIncidentsAreExplainedNotFixed(t *testing.T) {
	two := int32(2)
	yes := true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "report-2901", Namespace: tk.NS, UID: "job-1", CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "CronJob", Name: "report", UID: "cj", Controller: &yes}},
		},
		Spec: batchv1.JobSpec{BackoffLimit: &two, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "report"}}}}},
		Status: batchv1.JobStatus{Failed: 3, Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Minute))},
		}},
	}
	c, _ := tk.Cluster(t, "a", job, downNode("n1"))
	got := map[detect.Family]*incident.Incident{}
	for _, inc := range incident.NewStore(nil, nil).Observe(c.Name, c.Scan(time.Now()).Observations, time.Now()) {
		got[inc.Family] = inc
	}
	jobInc, nodeInc := got[detect.FamilyJob], got[detect.FamilyNode]
	if jobInc == nil || nodeInc == nil {
		t.Fatalf("want a job and a node incident, got %v", got)
	}
	if jobInc.Workload != (fleet.Ref{Kind: "CronJob", Name: "report"}) {
		t.Errorf("a CronJob's failed run belongs to the CronJob, got %s", jobInc.Workload)
	}
	if plan := planner().Plan(ctx, c, jobInc); plan.Action != nil || !strings.Contains(plan.Manual, "next scheduled run") {
		t.Errorf("job plan = %+v", plan)
	}
	if nodeInc.Namespace != "" || nodeInc.Workload.Kind != "Node" {
		t.Errorf("node incident = %s in %q", nodeInc.Workload, nodeInc.Namespace)
	}
	if plan := planner().Plan(ctx, c, nodeInc); plan.Action != nil || !strings.Contains(plan.Manual, "cannot repair a node") {
		t.Errorf("node plan = %+v", plan)
	}
}

func TestLaterSuccessfulRunClearsACronJobFailure(t *testing.T) {
	yes := true
	owner := []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "CronJob", Name: "report", UID: "cj", Controller: &yes}}
	failed := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "report-1", Namespace: tk.NS, UID: "j1", OwnerReferences: owner, CreationTimestamp: metav1.NewTime(time.Now().Add(-20 * time.Minute))},
		Status: batchv1.JobStatus{Failed: 1, Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", LastTransitionTime: metav1.NewTime(time.Now().Add(-15 * time.Minute))},
		}},
	}
	done := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	ok := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "report-2", Namespace: tk.NS, UID: "j2", OwnerReferences: owner, CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute))},
		Status:     batchv1.JobStatus{Succeeded: 1, CompletionTime: &done},
	}
	c, _ := tk.Cluster(t, "a", failed, ok)
	if obs := c.Scan(time.Now()).Observations; len(obs) != 0 {
		t.Fatalf("the CronJob has since succeeded; want nothing reported, got %+v", obs)
	}
}
