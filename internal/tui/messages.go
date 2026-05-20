// SPDX-License-Identifier: Apache-2.0

package tui

import corev1 "k8s.io/api/core/v1"

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

// ErrMsg surfaces a non-fatal error (e.g. transient watch retry) to be
// rendered in the footer status area without crashing the program.
type ErrMsg struct {
	Err error
}
