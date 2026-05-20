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
