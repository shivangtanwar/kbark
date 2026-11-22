// SPDX-License-Identifier: Apache-2.0

package detect

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func node(conds ...corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{Status: corev1.NodeStatus{Conditions: conds}}
}

func cond(t corev1.NodeConditionType, s corev1.ConditionStatus, age time.Duration) corev1.NodeCondition {
	return corev1.NodeCondition{Type: t, Status: s, LastTransitionTime: ago(age)}
}

func TestNode(t *testing.T) {
	tests := []struct {
		name      string
		node      *corev1.Node
		wantCause string // "" means healthy
	}{
		{"ready", node(cond(corev1.NodeReady, corev1.ConditionTrue, time.Hour)), ""},
		{"a missed heartbeat is not a failure", node(cond(corev1.NodeReady, corev1.ConditionUnknown, 20*time.Second)), ""},
		{"kubelet stopped reporting", node(cond(corev1.NodeReady, corev1.ConditionUnknown, 5*time.Minute)), CauseNotReady},
		{"not ready", node(cond(corev1.NodeReady, corev1.ConditionFalse, 5*time.Minute)), CauseNotReady},
		{"disk pressure", node(cond(corev1.NodeReady, corev1.ConditionTrue, time.Hour), cond(corev1.NodeDiskPressure, corev1.ConditionTrue, time.Minute)), CauseDiskPressure},
		{"memory pressure", node(cond(corev1.NodeReady, corev1.ConditionTrue, time.Hour), cond(corev1.NodeMemoryPressure, corev1.ConditionTrue, time.Minute)), CauseMemoryPressure},
		{"not ready outranks pressure", node(cond(corev1.NodeDiskPressure, corev1.ConditionTrue, time.Minute), cond(corev1.NodeReady, corev1.ConditionFalse, 5*time.Minute)), CauseNotReady},
		{"pressure that is false", node(cond(corev1.NodeReady, corev1.ConditionTrue, time.Hour), cond(corev1.NodeDiskPressure, corev1.ConditionFalse, time.Minute)), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := Node(tt.node, now)
			if tt.wantCause == "" {
				if ok {
					t.Fatalf("want healthy, got %+v", f)
				}
				return
			}
			if !ok || f.Family != FamilyNode || f.Cause != tt.wantCause || f.Summary == "" || len(f.Evidence) == 0 {
				t.Fatalf("got ok=%v %+v, want cause %q", ok, f, tt.wantCause)
			}
		})
	}
}

func TestTerminating(t *testing.T) {
	deleted := func(overdue time.Duration, finalizers ...string) *corev1.Pod {
		p := basePod()
		d := ago(overdue)
		p.DeletionTimestamp, p.Finalizers = &d, finalizers
		return p
	}
	if _, ok := Terminating(basePod(), now); ok {
		t.Error("a pod that was not deleted is not terminating")
	}
	if _, ok := Terminating(deleted(30*time.Second), now); ok {
		t.Error("a pod just past its deadline is still within grace")
	}
	f, ok := Terminating(deleted(10*time.Minute), now)
	if !ok || f.Family != FamilyTerminating || f.Cause != CauseUnknown {
		t.Errorf("stuck pod: ok=%v %+v", ok, f)
	}
	f, ok = Terminating(deleted(10*time.Minute, "example.com/cleanup"), now)
	if !ok || f.Cause != CauseFinalizer {
		t.Errorf("finalizer: ok=%v %+v", ok, f)
	}
	// The pod detector must leave deleted pods to this one.
	if got := Pod(deleted(10*time.Minute), nil, now); got != nil {
		t.Errorf("Pod() must ignore deleted pods, got %+v", got)
	}
}

func TestJob(t *testing.T) {
	failed := func(reason string, age time.Duration) *batchv1.Job {
		two := int32(2)
		return &batchv1.Job{
			Spec: batchv1.JobSpec{BackoffLimit: &two, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "report"}}}}},
			Status: batchv1.JobStatus{Failed: 3, Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: reason, LastTransitionTime: ago(age)},
			}},
		}
	}
	if _, ok := Job(&batchv1.Job{}, now); ok {
		t.Error("a running job is not failed")
	}
	done := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}}
	if _, ok := Job(done, now); ok {
		t.Error("a completed job is not failed")
	}
	if _, ok := Job(failed("BackoffLimitExceeded", 3*time.Hour), now); ok {
		t.Error("an old failure is no longer reported")
	}
	f, ok := Job(failed("BackoffLimitExceeded", time.Minute), now)
	if !ok || f.Cause != CauseBackoffLimit || f.Container != "report" {
		t.Errorf("backoff: ok=%v %+v", ok, f)
	}
	if f, ok := Job(failed("DeadlineExceeded", time.Minute), now); !ok || f.Cause != CauseDeadline {
		t.Errorf("deadline: ok=%v %+v", ok, f)
	}
}
