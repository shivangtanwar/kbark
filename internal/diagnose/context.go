// SPDX-License-Identifier: Apache-2.0

// Package diagnose builds the context payload kbark sends to the AI when
// the user presses `?` on a resource, and orchestrates the streaming
// session that returns the diagnosis.
//
// M5 ships a one-shot pattern: assemble everything we can up front, pass
// it to the model, stream the answer. M6 adds a tool-call loop where the
// model fetches more context as needed.
package diagnose

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LogTailLines is how many trailing log lines to include in the context.
// Enough to catch a stack trace, small enough to stay under typical token
// budgets when the lines are long.
const LogTailLines int64 = 200

// LogReadTimeout caps how long we wait for the log read to complete.
// A slow or unreachable log endpoint shouldn't block the diagnosis from
// starting — we'd rather diagnose with describe + events than not at all.
const LogReadTimeout = 3 * time.Second

// EventListLimit caps how many events we ask the API for. Re-sorted by
// LastTimestamp descending and trimmed to the most recent few before
// inclusion in the payload.
const EventListLimit = 20

// EventsInPayload is the upper bound on events we include in the final
// payload after sorting. The most recent events dominate diagnosis value.
const EventsInPayload = 10

// PodContextBuilder assembles a markdown-ish text payload for a single
// pod. The output is what the AI sees as its "ground truth" before
// generating a diagnosis.
type PodContextBuilder struct {
	client kubernetes.Interface
}

func NewPodContextBuilder(client kubernetes.Interface) *PodContextBuilder {
	return &PodContextBuilder{client: client}
}

// Build returns the full text payload. Best-effort: missing pieces (event
// list permission denied, log read times out) are dropped from the output
// rather than failing the whole call.
func (b *PodContextBuilder) Build(ctx context.Context, pod *corev1.Pod) string {
	var out strings.Builder

	fmt.Fprintf(&out, "## Pod: %s/%s\n\n", pod.Namespace, pod.Name)
	writeStatus(&out, pod)
	writeContainers(&out, pod)

	if events, err := b.recentEvents(ctx, pod); err == nil && len(events) > 0 {
		writeEvents(&out, events)
	}
	if logs := b.recentLogs(ctx, pod); logs != "" {
		writeLogs(&out, logs)
	}

	return out.String()
}

func writeStatus(out *strings.Builder, pod *corev1.Pod) {
	fmt.Fprintln(out, "### Status")
	fmt.Fprintf(out, "Phase: %s\n", pod.Status.Phase)
	if pod.Status.Reason != "" {
		fmt.Fprintf(out, "Reason: %s\n", pod.Status.Reason)
	}
	if pod.Status.Message != "" {
		fmt.Fprintf(out, "Message: %s\n", pod.Status.Message)
	}
	if pod.DeletionTimestamp != nil {
		fmt.Fprintln(out, "DeletionTimestamp: set (pod is terminating)")
	}
	fmt.Fprintln(out)
}

func writeContainers(out *strings.Builder, pod *corev1.Pod) {
	if len(pod.Status.ContainerStatuses) == 0 {
		return
	}
	fmt.Fprintln(out, "### Containers")
	for _, cs := range pod.Status.ContainerStatuses {
		fmt.Fprintf(out, "- %s ready=%v restarts=%d", cs.Name, cs.Ready, cs.RestartCount)
		if cs.Image != "" {
			fmt.Fprintf(out, " image=%s", cs.Image)
		}
		switch {
		case cs.State.Waiting != nil:
			fmt.Fprintf(out, " waiting=%s", cs.State.Waiting.Reason)
			if cs.State.Waiting.Message != "" {
				fmt.Fprintf(out, " (%s)", cs.State.Waiting.Message)
			}
		case cs.State.Terminated != nil:
			fmt.Fprintf(out, " terminated=%s exit=%d", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
			if cs.State.Terminated.Message != "" {
				fmt.Fprintf(out, " (%s)", cs.State.Terminated.Message)
			}
		case cs.State.Running != nil:
			fmt.Fprintf(out, " running since=%s", cs.State.Running.StartedAt.Format(time.RFC3339))
		}
		if last := cs.LastTerminationState.Terminated; last != nil {
			fmt.Fprintf(out, " last_terminated=%s exit=%d", last.Reason, last.ExitCode)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out)
}

func writeEvents(out *strings.Builder, events []corev1.Event) {
	fmt.Fprintln(out, "### Recent events")
	for _, e := range events {
		when := e.LastTimestamp.Time
		if when.IsZero() {
			when = e.EventTime.Time
		}
		fmt.Fprintf(out, "- [%s] %s %s: %s\n",
			when.Format(time.RFC3339), e.Type, e.Reason, strings.TrimSpace(e.Message))
	}
	fmt.Fprintln(out)
}

func writeLogs(out *strings.Builder, logs string) {
	logs = strings.TrimRight(logs, "\n")
	fmt.Fprintln(out, "### Recent logs (tail 200)")
	fmt.Fprintln(out, "```")
	fmt.Fprintln(out, logs)
	fmt.Fprintln(out, "```")
}

func (b *PodContextBuilder) recentEvents(ctx context.Context, pod *corev1.Pod) ([]corev1.Event, error) {
	selector := fmt.Sprintf("involvedObject.name=%s,involvedObject.namespace=%s", pod.Name, pod.Namespace)
	list, err := b.client.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: selector,
		Limit:         EventListLimit,
	})
	if err != nil {
		return nil, err
	}
	events := list.Items
	sort.Slice(events, func(i, j int) bool {
		ti := mostRecent(events[i])
		tj := mostRecent(events[j])
		return ti.After(tj)
	})
	if len(events) > EventsInPayload {
		events = events[:EventsInPayload]
	}
	return events, nil
}

func mostRecent(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	return e.EventTime.Time
}

func (b *PodContextBuilder) recentLogs(ctx context.Context, pod *corev1.Pod) string {
	timedCtx, cancel := context.WithTimeout(ctx, LogReadTimeout)
	defer cancel()

	tail := LogTailLines
	req := b.client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		TailLines: &tail,
	})
	stream, err := req.Stream(timedCtx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return ""
	}
	return string(data)
}
