// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// LogContextDefaultWindow is the symmetric line window around the
// focus line (i.e. 20 lines before, the focus line itself, and 20
// after). Matches the strategy doc's "line + surrounding 20 lines"
// brief; the symmetric reading is "the surrounding 20 on each side"
// since a one-sided 20 would miss preceding context for an error
// that started further up the stream.
const LogContextDefaultWindow = 20

// LogContextBuilder assembles the AI payload for "? on a log line".
// Wraps PodContextBuilder so we still get the pod's status, container
// state, and events alongside the focal log window — the model needs
// both "what's the pod doing" and "what's this line saying" to give
// a useful answer.
type LogContextBuilder struct {
	podBuilder *PodContextBuilder
}

func NewLogContextBuilder(client kubernetes.Interface) *LogContextBuilder {
	return &LogContextBuilder{
		podBuilder: NewPodContextBuilder(client),
	}
}

// LogFocus carries the cursor line + its index + the surrounding
// window lines (already extracted by the caller from LogsView). The
// builder doesn't re-read logs from the API — the user has already
// seen the streaming buffer, so reusing it gives them a diagnosis
// over the exact content they were looking at.
type LogFocus struct {
	// Line is the focal log line under the cursor.
	Line string
	// Index is the 0-based position in the live log buffer (just
	// useful context for the model — "line 247 of the visible buffer").
	Index int
	// Window is the consecutive slice surrounding Line, inclusive of
	// Line itself. May contain fewer than 2*WindowSize+1 entries when
	// the cursor is near a buffer edge.
	Window []string
	// WindowStart is the 0-based index of Window[0] in the live buffer,
	// used to report absolute line numbers in the payload.
	WindowStart int
}

// Build returns the full text payload for log-focused diagnosis.
// Includes the pod context block (so tool calls remain useful) plus
// a clearly-marked focus block with the cursor line highlighted.
func (b *LogContextBuilder) Build(ctx context.Context, pod *corev1.Pod, container string, focus LogFocus) string {
	var out strings.Builder

	if pod != nil {
		// Pod context first — same shape PodContextBuilder produces
		// for the `?`-on-pod path. The model already knows how to
		// reason about this block.
		out.WriteString(b.podBuilder.Build(ctx, pod))
	}

	out.WriteString("## Log focus\n\n")
	if container != "" {
		fmt.Fprintf(&out, "Container: %s\n", container)
	}
	fmt.Fprintf(&out, "Focal line (live-buffer line %d):\n", focus.Index)
	fmt.Fprintln(&out, "```")
	fmt.Fprintln(&out, strings.TrimRight(focus.Line, "\n"))
	fmt.Fprintln(&out, "```")
	fmt.Fprintln(&out)

	if len(focus.Window) > 0 {
		fmt.Fprintf(&out, "Surrounding window (lines %d..%d, focal line marked with `>`):\n",
			focus.WindowStart, focus.WindowStart+len(focus.Window)-1)
		fmt.Fprintln(&out, "```")
		for i, line := range focus.Window {
			absIdx := focus.WindowStart + i
			prefix := "  "
			if absIdx == focus.Index {
				prefix = "> "
			}
			fmt.Fprintf(&out, "%s%s\n", prefix, strings.TrimRight(line, "\n"))
		}
		fmt.Fprintln(&out, "```")
	}

	return out.String()
}
