// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/ai"
	"github.com/shivangtanwar/kbark/internal/diagnose"
)

func TestDispatcher_listsTools(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	tools := d.Tools()
	names := map[string]bool{}
	for _, tt := range tools {
		names[tt.Name] = true
	}
	for _, want := range []string{
		diagnose.ToolGetEvents,
		diagnose.ToolGetLogs,
		diagnose.ToolGetPreviousLogs,
		diagnose.ToolDescribePod,
		diagnose.ToolGetResource,
	} {
		if !names[want] {
			t.Errorf("missing tool %q in registry; have %v", want, names)
		}
	}
}

func TestDispatcher_getEventsForPod(t *testing.T) {
	pod := podWithName("default", "p")
	ev1 := podEvent("default", "p", "Warning", "FailedScheduling", "0/1 nodes available")
	ev2 := podEvent("default", "p", "Normal", "Scheduled", "assigned to node-1")
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(pod, ev1, ev2), nil)

	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetEvents,
		Arguments: `{"namespace":"default","name":"p"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(out, "FailedScheduling") {
		t.Errorf("expected FailedScheduling in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Scheduled") {
		t.Errorf("expected Scheduled in output, got:\n%s", out)
	}
}

func TestDispatcher_getLogsReturnsFakeLogs(t *testing.T) {
	pod := podWithName("default", "p")
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(pod), nil)

	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetLogs,
		Arguments: `{"namespace":"default","name":"p","tail_lines":50}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(out, "fake logs") {
		t.Errorf("expected fake-clientset placeholder logs, got:\n%s", out)
	}
}

func TestDispatcher_describePodIncludesContainers(t *testing.T) {
	pod := podWithName("default", "p")
	pod.Status.Phase = corev1.PodRunning
	pod.Spec.Containers = []corev1.Container{{Name: "main", Image: "nginx"}}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name: "main", Ready: true, RestartCount: 0, Image: "nginx",
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		},
	}
	pod.Spec.Volumes = []corev1.Volume{
		{Name: "config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}},
		}},
	}
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(pod), nil)

	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolDescribePod,
		Arguments: `{"namespace":"default","name":"p"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(out, "### Containers") {
		t.Errorf("expected ### Containers section, got:\n%s", out)
	}
	if !strings.Contains(out, "main ready=true") {
		t.Errorf("expected container summary, got:\n%s", out)
	}
	if !strings.Contains(out, "### Volumes") {
		t.Errorf("expected volume summary, got:\n%s", out)
	}
	if !strings.Contains(out, "configMap=app-config") {
		t.Errorf("expected configMap reference, got:\n%s", out)
	}
}

func TestDispatcher_unknownToolErrors(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	_, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      "wat",
		Arguments: `{}`,
	})
	if err == nil {
		t.Fatal("expected error for unknown tool name")
	}
}

func TestDispatcher_malformedJSONErrors(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	_, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetEvents,
		Arguments: `not json {`,
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON arguments")
	}
}

func TestDispatcher_getResourceWithoutDynamicClientReturnsHelpfulMessage(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetResource,
		Arguments: `{"namespace":"default","kind":"ConfigMap","name":"x"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(out, "dynamic client not available") {
		t.Errorf("expected helpful message about dynamic client, got:\n%s", out)
	}
}

// TestDispatcher_getEventsMissingFieldsReturnsHumanMessage pins the
// contract that argument-validation failures surface as a human
// message in the result string (not as a returned error), so the
// model can self-correct rather than treating it as a crash.
func TestDispatcher_getEventsMissingFieldsReturnsHumanMessage(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetEvents,
		Arguments: `{"namespace":"default"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch should not error on missing fields, got %v", err)
	}
	if !strings.Contains(out, "namespace and name are required") {
		t.Errorf("expected validation message, got %q", out)
	}
}

// TestDispatcher_getEventsNoMatchesReturnsExplicitMessage pins the
// no-events shape — the model should see "no events" verbatim
// rather than an empty string (which would invite hallucination).
func TestDispatcher_getEventsNoMatchesReturnsExplicitMessage(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(podWithName("default", "p")), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetEvents,
		Arguments: `{"namespace":"default","name":"p"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(out, "no events") {
		t.Errorf("expected 'no events' message, got %q", out)
	}
}

func TestDispatcher_getLogsPreviousFlagDispatches(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(podWithName("default", "p")), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetPreviousLogs,
		Arguments: `{"namespace":"default","name":"p"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// fake clientset's log stream returns "fake logs" regardless of the
	// `previous` flag — but the dispatch path must not error, which is
	// what we're guarding.
	if !strings.Contains(out, "fake logs") {
		t.Errorf("expected fake logs in previous-logs output, got %q", out)
	}
}

func TestDispatcher_getLogsMissingFieldsReturnsHumanMessage(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolGetLogs,
		Arguments: `{}`,
	})
	if err != nil {
		t.Fatalf("Dispatch should not error on missing fields, got %v", err)
	}
	if !strings.Contains(out, "namespace and name are required") {
		t.Errorf("expected validation message, got %q", out)
	}
}

func TestDispatcher_describePodMissingFieldsReturnsHumanMessage(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolDescribePod,
		Arguments: `{"namespace":"default"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch should not error on missing fields, got %v", err)
	}
	if !strings.Contains(out, "namespace and name are required") {
		t.Errorf("expected validation message, got %q", out)
	}
}

func TestDispatcher_describePodNonExistentReturnsLookupFailureMessage(t *testing.T) {
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolDescribePod,
		Arguments: `{"namespace":"default","name":"ghost"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch should not return Go error for missing pod, got %v", err)
	}
	if !strings.Contains(out, "pod lookup failed") {
		t.Errorf("expected lookup-failed message, got %q", out)
	}
}

// TestDispatcher_describePodWaitingStateRendersReason pins the
// CrashLoopBackOff / ImagePullBackOff path that the diagnose flow
// hits most often — the waiting reason is what tells the model
// "this is a crash loop", and the message that accompanies it is
// what tells the model WHY.
func TestDispatcher_describePodWaitingStateRendersReason(t *testing.T) {
	pod := podWithName("default", "stuck")
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name: "c", Ready: false, RestartCount: 42, Image: "busybox",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "CrashLoopBackOff", Message: "back-off 5m0s",
			}},
		},
	}
	d := diagnose.NewDispatcher(fake.NewSimpleClientset(pod), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolDescribePod,
		Arguments: `{"namespace":"default","name":"stuck"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for _, want := range []string{"waiting=CrashLoopBackOff", "back-off 5m0s", "restarts=42"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestDispatcher_redactsSecretsInToolOutput pins the M8.3 wiring
// through the dispatcher: a credential-looking value in any tool's
// output should be scrubbed before the model sees it. The dispatcher
// applies redact.Redact() inside Dispatch's truncation path; this
// confirms the call site is wired and the scrub fires end-to-end.
func TestDispatcher_redactsSecretsInToolOutput(t *testing.T) {
	pod := podWithName("default", "p")
	pod.Status.Reason = "ConfigError"
	pod.Status.Message = "init failed: DB_PASSWORD=super-secret-value-do-not-leak"

	d := diagnose.NewDispatcher(fake.NewSimpleClientset(pod), nil)
	out, err := d.Dispatch(context.Background(), ai.ToolCallEvent{
		Name:      diagnose.ToolDescribePod,
		Arguments: `{"namespace":"default","name":"p"}`,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if strings.Contains(out, "super-secret-value-do-not-leak") {
		t.Errorf("password value leaked through dispatcher:\n%s", out)
	}
	if !strings.Contains(out, "<redacted>") {
		t.Errorf("expected <redacted> placeholder in dispatcher output:\n%s", out)
	}
}

func podWithName(ns, name string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func podEvent(ns, podName, evtType, reason, message string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: podName + "-" + reason, Namespace: ns},
		InvolvedObject: corev1.ObjectReference{Namespace: ns, Name: podName, Kind: "Pod"},
		Type:           evtType,
		Reason:         reason,
		Message:        message,
		LastTimestamp:  metav1.Now(),
	}
}
