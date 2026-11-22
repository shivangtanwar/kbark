// SPDX-License-Identifier: Apache-2.0

package remediate

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shivangtanwar/kbark/internal/gitops"
	"github.com/shivangtanwar/kbark/internal/incident"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

// The planner and the manifest editor were written separately. These
// tests feed what the planner really produces into the editor, so a
// parameter one side renames cannot go unnoticed.

func asChange(inc *incident.Incident, a *incident.Action) gitops.Change {
	return gitops.Change{Kind: a.TargetKind, Namespace: inc.Namespace, Name: a.TargetName, Type: a.Type, Params: a.Params}
}

const cartManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: NAME
spec:
  template:
    spec:
      containers:
        - name: app
          image: IMAGE
          imagePullPolicy: Never
          resources:
            requests: {memory: 64Mi}
            limits: {memory: 64Mi}
          readinessProbe:
            exec: {command: [check]}
            timeoutSeconds: 1
`

func manifest(name, image string) []byte {
	return []byte(strings.NewReplacer("NAME", name, "IMAGE", image).Replace(cartManifest))
}

func TestPlannedActionsAreExpressibleAsManifestEdits(t *testing.T) {
	gitPlanner := func() *Planner { p := planner(); p.GitOps = true; return p }
	annotate := func(m *metav1.ObjectMeta) {
		m.Annotations = map[string]string{incident.AnnotationGitRepo: "acme/shop", incident.AnnotationGitPath: "apps/x.yaml"}
	}

	t.Run("memory limit", func(t *testing.T) {
		d := tk.Deployment("cart", tk.Container("cart:1"))
		annotate(&d.ObjectMeta)
		rs := tk.ReplicaSet(d, "1", tk.Container("cart:1"))
		c, _ := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "x", tk.OOMKilled()))
		inc := incidentFor(t, c)
		a := gitPlanner().Plan(ctx, c, inc).Action
		if a == nil || a.Source == nil || a.Source.Repo != "acme/shop" {
			t.Fatalf("want a proposal with a Git source, got %+v", a)
		}
		out, err := gitops.Edit(manifest("cart", "cart:1"), asChange(inc, a))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(out), "128Mi") != 2 {
			t.Errorf("limit and the equal request must both become 128Mi:\n%s", out)
		}
	})

	t.Run("pull policy", func(t *testing.T) {
		ctr := tk.Container("tool:1")
		ctr.ImagePullPolicy = corev1.PullNever
		d := tk.Deployment("tool", ctr)
		annotate(&d.ObjectMeta)
		rs := tk.ReplicaSet(d, "1", ctr)
		pod := tk.Pod(rs, "x", corev1.ContainerStatus{Image: "tool:1", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImageNeverPull"}}})
		pod.Status.Phase = corev1.PodPending
		c, _ := tk.Cluster(t, "a", d, rs, pod)
		inc := incidentFor(t, c)
		a := gitPlanner().Plan(ctx, c, inc).Action
		out, err := gitops.Edit(manifest("tool", "tool:1"), asChange(inc, a))
		if err != nil || !strings.Contains(string(out), "imagePullPolicy: IfNotPresent") {
			t.Fatalf("err=%v\n%s", err, out)
		}
	})

	t.Run("readiness timeout", func(t *testing.T) {
		ctr := tk.Container("api:1")
		ctr.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"check"}}}, TimeoutSeconds: 1}
		d := tk.Deployment("api", ctr)
		annotate(&d.ObjectMeta)
		rs := tk.ReplicaSet(d, "1", ctr)
		pod := tk.Pod(rs, "x", corev1.ContainerStatus{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Now().Add(-3 * time.Minute))}}})
		ev := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "ev1", Namespace: tk.NS}, InvolvedObject: corev1.ObjectReference{UID: pod.UID, FieldPath: "spec.containers{app}"},
			Reason: "Unhealthy", Message: `Readiness probe failed: command timed out: "check" timed out after 1s`, Count: 20, LastTimestamp: metav1.Now(),
		}
		c, _ := tk.Cluster(t, "a", d, rs, pod, ev)
		inc := incidentFor(t, c)
		a := gitPlanner().Plan(ctx, c, inc).Action
		out, err := gitops.Edit(manifest("api", "api:1"), asChange(inc, a))
		if err != nil || !strings.Contains(string(out), "timeoutSeconds: 5") {
			t.Fatalf("err=%v\n%s", err, out)
		}
	})

	t.Run("service selector", func(t *testing.T) {
		d := tk.Deployment("checkout", tk.Container("c:1"))
		rs := tk.ReplicaSet(d, "1", tk.Container("c:1"))
		svc := service("checkout", map[string]string{"app": "check-out"})
		annotate(&svc.ObjectMeta)
		c, _ := tk.Cluster(t, "a", d, rs, tk.Pod(rs, "a", running()), svc)
		inc := incidentFor(t, c)
		a := gitPlanner().Plan(ctx, c, inc).Action
		if a == nil || a.Source == nil {
			t.Fatalf("want a selector fix with a Git source, got %+v", a)
		}
		file := []byte("apiVersion: v1\nkind: Service\nmetadata:\n  name: checkout\nspec:\n  selector:\n    app: check-out\n  ports:\n    - port: 80\n")
		out, err := gitops.Edit(file, asChange(inc, a))
		if err != nil || !strings.Contains(string(out), "app: checkout") {
			t.Fatalf("err=%v\n%s", err, out)
		}
	})

	t.Run("a rollback has no source", func(t *testing.T) {
		d := tk.Deployment("api", tk.Container("api:2"))
		annotate(&d.ObjectMeta)
		prev, cur := tk.ReplicaSet(d, "1", tk.Container("api:1")), tk.ReplicaSet(d, "2", tk.Container("api:2"))
		c, _ := tk.Cluster(t, "a", d, prev, cur, tk.Pod(cur, "x", tk.Crashing()))
		a := gitPlanner().Plan(ctx, c, incidentFor(t, c)).Action
		if a == nil || a.Type != incident.ActionRollbackWorkload || a.Source != nil {
			t.Fatalf("a rollback is not a file edit and must not offer a pull request: %+v", a)
		}
	})
}
