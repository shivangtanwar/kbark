// SPDX-License-Identifier: Apache-2.0

package describe_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shivangtanwar/kbark/internal/describe"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

func TestService_YAMLProducesExpectedShape(t *testing.T) {
	svc := describe.NewService(nil)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "default", Labels: map[string]string{"app": "demo"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "c", Image: "busybox"},
		}},
	}
	out, err := svc.YAML(pod, kinds.Pods())
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	for _, want := range []string{
		"apiVersion: v1",
		"kind: Pod",
		"name: alpha",
		"namespace: default",
		"app: demo",
		"image: busybox",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("YAML output missing %q.\nGot:\n%s", want, out)
		}
	}
}

// TestService_YAMLDoesNotMutateInput pins the deep-copy behaviour —
// the cached informer object whose TypeMeta we stamp must remain
// untouched after YAML serialisation. A shared cache mutation here
// would corrupt the table rendering on the next snapshot.
func TestService_YAMLDoesNotMutateInput(t *testing.T) {
	svc := describe.NewService(nil)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "default"},
	}
	originalKind := pod.GetObjectKind().GroupVersionKind()
	if _, err := svc.YAML(pod, kinds.Pods()); err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if got := pod.GetObjectKind().GroupVersionKind(); got != originalKind {
		t.Errorf("YAML mutated input TypeMeta: was %v, now %v", originalKind, got)
	}
}

func TestService_YAMLNilObjectErrors(t *testing.T) {
	svc := describe.NewService(nil)
	if _, err := svc.YAML(nil, kinds.Pods()); err == nil {
		t.Error("YAML(nil) returned no error; expected one")
	}
}

// TestService_YAMLStripsManagedFields pins the readability fix:
// Kubernetes objects accumulate verbose managedFields entries that
// dominate the YAML view (often 100+ lines per controller edit). We
// strip them so the user sees the parts they actually care about
// (metadata, spec, status), the same way `kubectl get -o yaml
// --show-managed-fields=false` works.
func TestService_YAMLStripsManagedFields(t *testing.T) {
	svc := describe.NewService(nil)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alpha",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl", Operation: "Update", APIVersion: "v1"},
				{Manager: "kubelet", Operation: "Update", APIVersion: "v1"},
			},
		},
	}
	out, err := svc.YAML(pod, kinds.Pods())
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if strings.Contains(out, "managedFields") {
		t.Errorf("managedFields should be stripped from YAML output, got:\n%s", out)
	}
	if strings.Contains(out, "kubectl") || strings.Contains(out, "kubelet") {
		t.Errorf("manager names from managedFields should not appear, got:\n%s", out)
	}
}

// TestService_DescribeWithoutRESTGetterReturnsError pins the
// fallback behaviour when kubeconfig wiring failed at startup — the
// modal still works (YAML-only) and the describe call surfaces an
// actionable error instead of a crash.
func TestService_DescribeWithoutRESTGetterReturnsError(t *testing.T) {
	svc := describe.NewService(nil)
	_, err := svc.Describe(context.Background(), kinds.Pods(), "default", "alpha")
	if err == nil {
		t.Error("Describe(nil getter) returned no error")
	}
}
