// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/kube"
)

const (
	testNamespace = "default"
	// Generous timeout: informer initial sync against the fake clientset
	// is usually microseconds, but CI runners can be slow under load.
	snapshotTimeout = 500 * time.Millisecond
)

func TestPodLister_emitsOnAddUpdateDelete(t *testing.T) {
	cs := fake.NewSimpleClientset()
	factory := kube.NewFactory(cs, testNamespace, kube.DefaultResyncInterval)
	pl := kube.NewPodLister(factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pl.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Initial snapshot — bump() at end of Start() fires a snapshot even on empty cluster.
	want := waitForSnapshot(t, pl, "initial")
	if got := len(want); got != 0 {
		t.Errorf("initial snapshot should be empty, got %d pods", got)
	}

	// --- Add ---
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: testNamespace},
	}
	if _, err := cs.CoreV1().Pods(testNamespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	pods := waitForSnapshot(t, pl, "after Add")
	if len(pods) != 1 || pods[0].Name != "alpha" {
		t.Fatalf("after Add: expected [alpha], got %s", names(pods))
	}

	// --- Update ---
	pod.Labels = map[string]string{"updated": "true"}
	if _, err := cs.CoreV1().Pods(testNamespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update pod: %v", err)
	}
	pods = waitForSnapshot(t, pl, "after Update")
	if len(pods) != 1 || pods[0].Labels["updated"] != "true" {
		t.Fatalf("after Update: expected label set, got %v", pods[0].Labels)
	}

	// --- Delete ---
	if err := cs.CoreV1().Pods(testNamespace).Delete(ctx, "alpha", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	pods = waitForSnapshot(t, pl, "after Delete")
	if len(pods) != 0 {
		t.Fatalf("after Delete: expected empty, got %s", names(pods))
	}
}

func TestPodLister_snapshotsSortedByName(t *testing.T) {
	cs := fake.NewSimpleClientset()
	factory := kube.NewFactory(cs, testNamespace, kube.DefaultResyncInterval)
	pl := kube.NewPodLister(factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := pl.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Drain initial empty snapshot.
	_ = waitForSnapshot(t, pl, "initial")

	for _, name := range []string{"gamma", "alpha", "beta"} {
		if _, err := cs.CoreV1().Pods(testNamespace).Create(
			ctx,
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace}},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	// After debounce, exactly one snapshot containing all three, sorted.
	pods := waitForSnapshot(t, pl, "after batch add")
	if names(pods) != "alpha,beta,gamma" {
		t.Errorf("expected alpha,beta,gamma, got %s", names(pods))
	}
}

func waitForSnapshot(t *testing.T, pl *kube.PodLister, label string) []*corev1.Pod {
	t.Helper()
	select {
	case s := <-pl.Snapshots():
		return s
	case <-time.After(snapshotTimeout):
		t.Fatalf("no snapshot %s within %v", label, snapshotTimeout)
		return nil
	}
}

func names(pods []*corev1.Pod) string {
	out := ""
	for i, p := range pods {
		if i > 0 {
			out += ","
		}
		out += p.Name
	}
	return out
}
