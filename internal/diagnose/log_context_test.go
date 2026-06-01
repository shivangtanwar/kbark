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

func TestLogContextBuilder_buildIncludesPodAndFocus(t *testing.T) {
	cs := fake.NewSimpleClientset()
	b := diagnose.NewLogContextBuilder(cs)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cause-crash", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: false, RestartCount: 7, Image: "busybox"},
			},
		},
	}
	focus := diagnose.LogFocus{
		Line:        "panic: missing config volume",
		Index:       42,
		WindowStart: 40,
		Window: []string{
			"INFO loading config",
			"WARN config path empty",
			"panic: missing config volume",
		},
	}

	out := b.Build(context.Background(), pod, "app", focus)

	for _, want := range []string{
		"default/cause-crash",            // pod context block present
		"## Log focus",                   // focus header
		"Container: app",                 // container name
		"line 42",                        // index in focal-line label
		"panic: missing config volume",   // focal line text
		"> panic: missing config volume", // window marker on focal line
		"  INFO loading config",          // non-focal window prefix
		"lines 40..42",                   // window range label
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q.\nGot:\n%s", want, out)
		}
	}
}

// TestLogContextBuilder_nilPodSkipsPodBlock pins the defensive path —
// if the pod somehow can't be resolved (deleted between `?` press and
// payload build), we still produce a usable log-focus payload.
func TestLogContextBuilder_nilPodSkipsPodBlock(t *testing.T) {
	b := diagnose.NewLogContextBuilder(fake.NewSimpleClientset())
	out := b.Build(context.Background(), nil, "app", diagnose.LogFocus{
		Line:        "x",
		Index:       0,
		Window:      []string{"x"},
		WindowStart: 0,
	})
	if strings.Contains(out, "## Pod") {
		t.Error("nil pod should not produce a Pod section")
	}
	if !strings.Contains(out, "## Log focus") {
		t.Error("focus section must still be present")
	}
}
