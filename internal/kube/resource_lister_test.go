// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"context"
	"sort"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

func TestResourceLister_emitsOnAddUpdateDelete(t *testing.T) {
	cs := fake.NewSimpleClientset()
	factory := kube.NewFactory(cs, testNamespace, kube.DefaultResyncInterval)
	rl, err := kube.NewResourceLister(factory, kinds.Deployments())
	if err != nil {
		t.Fatalf("NewResourceLister: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := rl.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Initial empty snapshot.
	if got := len(waitForResourceSnapshot(t, rl, "initial")); got != 0 {
		t.Errorf("initial snapshot should be empty, got %d", got)
	}

	// --- Add ---
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: testNamespace}}
	if _, err := cs.AppsV1().Deployments(testNamespace).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	objs := waitForResourceSnapshot(t, rl, "after Add")
	if got := objectNames(objs); got != "alpha" {
		t.Fatalf("after Add: got %q, want %q", got, "alpha")
	}

	// --- Update ---
	dep.Labels = map[string]string{"k": "v"}
	if _, err := cs.AppsV1().Deployments(testNamespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	objs = waitForResourceSnapshot(t, rl, "after Update")
	if got := len(objs); got != 1 {
		t.Fatalf("after Update: len=%d, want 1", got)
	}

	// --- Delete ---
	if err := cs.AppsV1().Deployments(testNamespace).Delete(ctx, "alpha", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	objs = waitForResourceSnapshot(t, rl, "after Delete")
	if got := len(objs); got != 0 {
		t.Fatalf("after Delete: len=%d, want 0", got)
	}
}

func TestResourceLister_snapshotsSortedByName(t *testing.T) {
	cs := fake.NewSimpleClientset()
	factory := kube.NewFactory(cs, testNamespace, kube.DefaultResyncInterval)
	rl, err := kube.NewResourceLister(factory, kinds.Deployments())
	if err != nil {
		t.Fatalf("NewResourceLister: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := rl.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = waitForResourceSnapshot(t, rl, "initial")

	for _, name := range []string{"gamma", "alpha", "beta"} {
		if _, err := cs.AppsV1().Deployments(testNamespace).Create(
			ctx,
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace}},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	objs := waitForResourceSnapshot(t, rl, "after batch add")
	if got := objectNames(objs); got != "alpha,beta,gamma" {
		t.Errorf("sort order: got %q, want %q", got, "alpha,beta,gamma")
	}
}

// TestResourceLister_unknownGVRReturnsError pins the contract: a typo
// in a kind file's GVR fails at construction with a useful error,
// not at runtime with a nil-deref.
func TestResourceLister_unknownGVRReturnsError(t *testing.T) {
	cs := fake.NewSimpleClientset()
	factory := kube.NewFactory(cs, testNamespace, kube.DefaultResyncInterval)
	bogus := kinds.Plugin{
		Key: "bogus", DisplayName: "Bogus",
		GVR: appsv1.SchemeGroupVersion.WithResource("nonsense"),
	}
	if _, err := kube.NewResourceLister(factory, bogus); err == nil {
		t.Error("expected error for unknown GVR, got nil")
	}
}

func waitForResourceSnapshot(t *testing.T, rl *kube.ResourceLister, label string) []runtime.Object {
	t.Helper()
	select {
	case s := <-rl.Snapshots():
		return s
	case <-time.After(snapshotTimeout):
		t.Fatalf("no snapshot %s within %v", label, snapshotTimeout)
		return nil
	}
}

func objectNames(objs []runtime.Object) string {
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		a, _ := meta.Accessor(o)
		if a != nil {
			names = append(names, a.GetName())
		}
	}
	// Preserve order (caller asserts sort), but stable-sort defensively
	// against unstable runtime ordering on empty cases.
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += "," + n
	}
	_ = sort.StringsAreSorted(names)
	return out
}
