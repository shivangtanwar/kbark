// SPDX-License-Identifier: Apache-2.0

//go:build envtest

// Package kube envtest tests boot a real (in-memory) Kubernetes
// apiserver + etcd via controller-runtime's envtest. Gated behind
// the `envtest` build tag so `go test ./...` doesn't fail on
// machines without the apiserver / etcd binaries; CI opts in
// explicitly. Setup recipe lives in CONTRIBUTING.md.

package kube_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// TestEnd2End_resourceListerEmitsRealApiserverSnapshots is the M11.2
// acceptance criterion: one test exercising the kube layer against a
// real Kubernetes apiserver instead of the fake clientset. Catches
// regressions in the informer wiring that fakes wouldn't surface
// (watch protocol changes, list-then-watch semantics, etc).
//
// What it does
//   - Boots envtest (in-memory apiserver + etcd).
//   - Creates a Pod via the typed clientset.
//   - Starts a ResourceLister scoped to "default", waits for the
//     initial snapshot, asserts the Pod is in it.
//   - Updates the Pod (label change), waits for the next snapshot,
//     asserts the update propagated.
//   - Shuts envtest down cleanly.
//
// The whole test target is ~5–10s end-to-end on CI; the acceptance
// criterion is "envtest run completes in CI under 90s" — we have
// comfortable margin.
func TestEnd2End_resourceListerEmitsRealApiserverSnapshots(t *testing.T) {
	env := &envtest.Environment{
		// BinaryAssetsDirectory is auto-resolved from KUBEBUILDER_ASSETS
		// (set by setup-envtest in CI); the test will fail-fast with a
		// clear message if it isn't set.
		BinaryAssetsDirectory: filepath.Join("..", "..", ".envtest-bins"),
		ErrorIfCRDPathMissing: false,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest start: %v (have you run `task envtest-setup` and exported KUBEBUILDER_ASSETS?)", err)
	}
	t.Cleanup(func() {
		if stopErr := env.Stop(); stopErr != nil {
			t.Logf("envtest stop: %v", stopErr)
		}
	})

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-pod",
			Namespace: "default",
			Labels:    map[string]string{"phase": "initial"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
		},
	}
	if _, err := clientset.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	svc := kube.NewResourceService(clientset, kube.DefaultResyncInterval, ctx, kinds.Pods())
	ch, _, err := svc.Switch("default")
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}

	first := waitSnapshot(t, ch, "initial")
	if !containsPodNamed(first, "e2e-pod") {
		t.Fatalf("initial snapshot missing e2e-pod, got %d objects", len(first))
	}

	// Mutate and assert the next snapshot reflects it. Informer event
	// handler should fire on Update; debounce window is 100ms.
	updated := first[0].(*corev1.Pod).DeepCopy()
	updated.Labels["phase"] = "updated"
	if _, err := clientset.CoreV1().Pods("default").Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update pod: %v", err)
	}

	second := waitSnapshot(t, ch, "after update")
	pp := findPod(second, "e2e-pod")
	if pp == nil {
		t.Fatal("after update: e2e-pod missing from snapshot")
	}
	if pp.Labels["phase"] != "updated" {
		t.Errorf("after update: phase label = %q, want \"updated\"", pp.Labels["phase"])
	}
}

func waitSnapshot(t *testing.T, ch <-chan []runtime.Object, label string) []runtime.Object {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(10 * time.Second):
		t.Fatalf("no snapshot %s within 10s", label)
		return nil
	}
}

func containsPodNamed(objs []runtime.Object, name string) bool {
	return findPod(objs, name) != nil
}

func findPod(objs []runtime.Object, name string) *corev1.Pod {
	for _, o := range objs {
		pod, ok := o.(*corev1.Pod)
		if !ok {
			continue
		}
		if pod.Name == name {
			return pod
		}
	}
	return nil
}
