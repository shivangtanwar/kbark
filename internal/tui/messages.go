// SPDX-License-Identifier: Apache-2.0

package tui

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/diagnose"
)

// PodsUpdatedMsg carries a fresh namespace-scoped pod snapshot from the
// informer. The slice is sorted by name and is the *full* current state,
// not a diff — the model replaces its data each time.
type PodsUpdatedMsg struct {
	Pods []*corev1.Pod
}

// NamespaceChangedMsg fires when the user selects a new namespace via
// the command bar. The TUI rebinds its informer to the new namespace.
type NamespaceChangedMsg struct {
	Namespace string
}

// LogsBatchMsg carries a debounced batch of log lines from the active
// log streamer. The view appends; it never replaces.
type LogsBatchMsg struct {
	Lines []string
}

// LogsEndMsg fires when the active log stream ends (EOF, error, or the
// stream's context cancelled). The view stops issuing waitForLogs Cmds.
type LogsEndMsg struct{}

// ResourceSnapshotMsg carries a fresh snapshot for a non-pod resource
// kind (e.g. deployments, services). Pods continue to use the typed
// PodsUpdatedMsg path because the diagnose `?` flow needs typed
// *corev1.Pod access — splitting at this seam lets new kinds land
// without disturbing the pod path.
type ResourceSnapshotMsg struct {
	Kind    string
	Objects []runtime.Object
}

// ResourceStreamEndMsg fires when the active resource snapshot stream
// ends (namespace switch or service teardown). The Model stops issuing
// waitForResource Cmds for that stream.
type ResourceStreamEndMsg struct {
	Kind string
}

// DiagnosisStartedMsg carries the session handle once the context has
// been built and the provider stream has opened. The Model stashes the
// session so Esc can cancel it.
type DiagnosisStartedMsg struct {
	Session *diagnose.Session
	Pod     *corev1.Pod
}

// DiagnosisDeltaMsg is a chunk of streamed text from the AI provider.
type DiagnosisDeltaMsg struct {
	Text string
}

// DiagnosisToolCallMsg fires when the model calls a tool mid-diagnosis.
// The Model must re-issue waitForDiagnoseEvent on receipt — without a
// message the bubbletea Cmd loop stops pumping the events channel, the
// session's buffer fills, and the whole diagnosis wedges. Carries the
// tool name for the M6.3 breadcrumb line.
type DiagnosisToolCallMsg struct {
	Name string
}

// DiagnosisDoneMsg fires when the AI provider emits its DoneEvent.
type DiagnosisDoneMsg struct {
	StopReason string
}

// DiagnosisErrorMsg fires when the AI provider emits an ErrorEvent or
// when context building / session start fails up front.
type DiagnosisErrorMsg struct {
	Err error
}

// DescribeReadyMsg carries the kubectl-style describe text once
// kubectl/describe has returned. The modal swaps "loading describe…"
// for this text.
type DescribeReadyMsg struct {
	Text string
}

// DescribeErrorMsg fires when kubectl/describe fails (apiserver
// hiccup, RBAC, missing kind). The modal stays open; the YAML view
// remains usable.
type DescribeErrorMsg struct {
	Err error
}

// ErrMsg surfaces a non-fatal error (e.g. transient watch retry) to be
// rendered in the footer status area without crashing the program.
type ErrMsg struct {
	Err error
}
