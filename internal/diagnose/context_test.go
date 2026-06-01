// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/diagnose"
)

func crashLoopPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cause-crash", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "main", Image: "busybox"},
		}},
		Status: corev1.PodStatus{
			Phase:  corev1.PodRunning,
			Reason: "",
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "main",
					Ready:        false,
					RestartCount: 5,
					Image:        "busybox:latest",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "back-off 5m0s restarting failed container",
						},
					},
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:   "Error",
							ExitCode: 1,
						},
					},
				},
			},
		},
	}
}

func TestPodContext_includesHeaderPhaseAndContainerStatus(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := diagnose.NewPodContextBuilder(cs)

	payload := b.Build(context.Background(), crashLoopPod())

	mustContain(t, payload, "## Pod: default/cause-crash")
	mustContain(t, payload, "Phase: Running")
	mustContain(t, payload, "### Containers")
	mustContain(t, payload, "main ready=false restarts=5")
	mustContain(t, payload, "waiting=CrashLoopBackOff")
	mustContain(t, payload, "last_terminated=Error exit=1")
}

func TestPodContext_omitsEventsHeaderWhenNoEvents(t *testing.T) {
	cs := fake.NewSimpleClientset() // no event objects created
	b := diagnose.NewPodContextBuilder(cs)

	payload := b.Build(context.Background(), crashLoopPod())

	if strings.Contains(payload, "### Recent events") {
		t.Errorf("payload should not include events header when none exist:\n%s", payload)
	}
}

func TestPodContext_includesLogsWhenAvailable(t *testing.T) {
	// kubernetes/fake's log stream returns the literal "fake logs" for any
	// pod. Good enough to verify the logs block is wired in.
	cs := fake.NewSimpleClientset()
	b := diagnose.NewPodContextBuilder(cs)

	payload := b.Build(context.Background(), crashLoopPod())

	mustContain(t, payload, "### Recent logs")
	mustContain(t, payload, "fake logs")
}

func TestPodContext_terminatingPodMarked(t *testing.T) {
	pod := crashLoopPod()
	now := metav1.Now()
	pod.DeletionTimestamp = &now

	cs := fake.NewSimpleClientset()
	b := diagnose.NewPodContextBuilder(cs)

	payload := b.Build(context.Background(), pod)
	mustContain(t, payload, "DeletionTimestamp: set (pod is terminating)")
}

// TestPodContext_redactsSecretsInEnv pins the M8.3 wiring: when the
// pod's container env contains a value that looks like a secret
// (DB_PASSWORD, JWT_SECRET, etc.), the builder's redaction pass must
// scrub it before the payload reaches the model. The container's
// regular env (image name, log level) flows through untouched.
//
// Note: the pod's Spec.Containers env doesn't end up in the current
// PodContextBuilder.Build output (which only covers status fields).
// This test simulates the equivalent by stuffing a secret-looking
// value into Status.Message — confirming the redactor is invoked on
// the assembled string and not bypassed.
func TestPodContext_redactsSecretsInEnv(t *testing.T) {
	pod := crashLoopPod()
	pod.Status.Reason = "Failed"
	pod.Status.Message = `Could not init: password: hunter2 (env DB_PASSWORD=hunter2)`

	cs := fake.NewSimpleClientset()
	b := diagnose.NewPodContextBuilder(cs)

	payload := b.Build(context.Background(), pod)

	if strings.Contains(payload, "hunter2") {
		t.Errorf("password value leaked into payload:\n%s", payload)
	}
	if !strings.Contains(payload, "<redacted>") {
		t.Errorf("redaction placeholder missing from payload:\n%s", payload)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("missing %q in payload:\n%s", needle, haystack)
	}
}
