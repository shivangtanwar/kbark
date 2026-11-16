// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shivangtanwar/kbark/internal/detect"
	"github.com/shivangtanwar/kbark/internal/fleet"
	tk "github.com/shivangtanwar/kbark/internal/testkit"
)

func healthy() corev1.ContainerStatus {
	return corev1.ContainerStatus{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
}

func TestScanGroupsPodsByWorkloadAndCounts(t *testing.T) {
	d := tk.Deployment("api", tk.Container("api:2"))
	rs := tk.ReplicaSet(d, "2", tk.Container("api:2"))
	ok := tk.Deployment("web", tk.Container("web:1"))
	okRS := tk.ReplicaSet(ok, "1", tk.Container("web:1"))
	c, _ := tk.Cluster(t, "a", d, rs, ok, okRS,
		tk.Pod(rs, "a", tk.Crashing()), tk.Pod(rs, "b", tk.Crashing()), tk.Pod(okRS, "a", healthy()), tk.Node("n1", false))

	snap := c.Scan(time.Now())
	if len(snap.Observations) != 1 {
		t.Fatalf("two crashing pods of one Deployment should be one observation, got %d", len(snap.Observations))
	}
	o := snap.Observations[0]
	if o.Workload != (fleet.Ref{Kind: "Deployment", Name: "api"}) || o.Finding.Family != detect.FamilyCrash || len(o.Pods) != 2 {
		t.Fatalf("unexpected observation: %+v", o)
	}
	s := snap.Summary
	if s.Pods != 3 || s.Failing != 2 || s.Healthy != 1 || s.Nodes != 1 || s.NodesReady != 1 {
		t.Fatalf("unexpected summary: %+v", s)
	}
}

func TestServiceSelectorNearMiss(t *testing.T) {
	d := tk.Deployment("checkout", tk.Container("c:1"))
	rs := tk.ReplicaSet(d, "1", tk.Container("c:1"))
	pod := tk.Pod(rs, "a", healthy())
	svc := func(name string, sel map[string]string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: tk.NS}, Spec: corev1.ServiceSpec{Selector: sel}}
	}
	c, _ := tk.Cluster(
		t, "a", d, rs, pod,
		svc("typo", map[string]string{"app": "check-out"}),      // near miss: reported
		svc("right", map[string]string{"app": "checkout"}),      // matches: fine
		svc("unrelated", map[string]string{"app": "inventory"}), // scaled to zero or elsewhere: not reported
	)
	snap := c.Scan(time.Now())
	if len(snap.Observations) != 1 || snap.Observations[0].Workload.Name != "typo" || snap.Observations[0].Finding.Family != detect.FamilyNetwork {
		t.Fatalf("want only the near-miss Service reported, got %+v", snap.Observations)
	}
}
