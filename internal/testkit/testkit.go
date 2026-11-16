// SPDX-License-Identifier: Apache-2.0

// Package testkit builds small Kubernetes object graphs and a synced
// fleet.Cluster over a fake clientset, for tests of the autofix layer.
package testkit

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/fleet"
)

// NS is the namespace every helper uses.
const NS = "shop"

// Cluster returns a started, synced cluster over the given objects.
func Cluster(t *testing.T, name string, objs ...runtime.Object) (*fleet.Cluster, *fake.Clientset) {
	t.Helper()
	client := fake.NewSimpleClientset(objs...)
	c := fleet.New(name, client)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Start(ctx)
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	if !c.WaitForSync(wctx) {
		t.Fatal("cluster caches did not sync")
	}
	return c, client
}

func controller(kind, name string, uid types.UID) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: kind, Name: name, UID: uid, Controller: &yes}}
}

// Container is a one-container pod spec with a 64Mi memory limit and a
// liveness probe.
func Container(image string) corev1.Container {
	return corev1.Container{
		Name: "app", Image: image,
		Resources: corev1.ResourceRequirements{
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{}},
			InitialDelaySeconds: 5,
		},
	}
}

// Deployment returns a rolled-out one-replica Deployment.
func Deployment(name string, ctr corev1.Container) *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: NS, UID: types.UID("dep-" + name), Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{ctr}},
			},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
}

// ReplicaSet returns the ReplicaSet for one revision of d, with ctr as
// that revision's container.
func ReplicaSet(d *appsv1.Deployment, rev string, ctr corev1.Container) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.Name + "-rev" + rev, Namespace: NS, UID: types.UID("rs-" + d.Name + rev),
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": rev},
			OwnerReferences: controller("Deployment", d.Name, d.UID),
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": d.Name, "pod-template-hash": "h" + rev}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{ctr}},
		}},
	}
}

// Pod returns a pod owned by rs running rs's template, with status st
// for its single container.
func Pod(rs *appsv1.ReplicaSet, suffix string, st corev1.ContainerStatus) *corev1.Pod {
	st.Name = "app"
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: rs.Name + "-" + suffix, Namespace: NS, UID: types.UID("pod-" + rs.Name + suffix),
			Labels: rs.Spec.Template.Labels, OwnerReferences: controller("ReplicaSet", rs.Name, rs.UID),
			CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
		Spec:   *rs.Spec.Template.Spec.DeepCopy(),
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{st}},
	}
}

// OOMKilled is the status of a container the kernel keeps killing.
func OOMKilled() corev1.ContainerStatus {
	return corev1.ContainerStatus{
		RestartCount: 4,
		State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-20 * time.Second)),
		}},
	}
}

// Crashing is the status of a container that exits 1 on every start.
func Crashing() corev1.ContainerStatus {
	return corev1.ContainerStatus{
		RestartCount: 5,
		State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "Error", ExitCode: 1, FinishedAt: metav1.NewTime(time.Now().Add(-20 * time.Second)),
		}},
	}
}

// PullFailing is the status of a container whose image tag does not exist.
func PullFailing(image string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Image: image,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "manifest unknown: not found"}},
	}
}

// Node returns a Ready node.
func Node(name string, cordoned bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("node-" + name)},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
}
