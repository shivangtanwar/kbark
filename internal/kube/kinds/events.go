// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Events is the plugin for core/v1 Events. Modern clusters also emit
// events to events.k8s.io/v1, but core/v1 is the lowest-common-denominator
// path and what kubectl reads by default for `kubectl get events`.
//
// v1 ships sorted by name (the abstraction's default). Time-descending
// ordering ("most recent first") is more natural for events but the
// abstraction doesn't have a per-plugin sort hook yet; if event browsing
// proves load-bearing in practice, M9 polish adds one.
func Events() Plugin {
	return Plugin{
		Key:         "ev",
		DisplayName: "Events",
		GVR:         corev1.SchemeGroupVersion.WithResource("events"),
		Columns: []table.Column{
			{Title: "LAST SEEN", Width: 12},
			{Title: "TYPE", Width: 9},
			{Title: "REASON", Width: 22},
			{Title: "OBJECT", Width: 30},
			{Title: "MESSAGE", Width: 50},
		},
		Row: func(obj runtime.Object) table.Row {
			e, ok := obj.(*corev1.Event)
			if !ok {
				return table.Row{"<malformed>", "", "", "", ""}
			}
			return table.Row{
				eventLastSeen(e),
				e.Type,
				e.Reason,
				eventObject(e),
				strings.TrimSpace(e.Message),
			}
		},
	}
}

// eventLastSeen prefers LastTimestamp (the standard field), falling
// back through FirstTimestamp and the resource creation time. The
// k8s.io/v1 events API uses different fields (Series, etc.); core/v1
// events that get those treated as out-of-band don't always populate
// LastTimestamp.
func eventLastSeen(e *corev1.Event) string {
	if !e.LastTimestamp.IsZero() {
		return components.FormatAge(e.LastTimestamp.Time)
	}
	if !e.FirstTimestamp.IsZero() {
		return components.FormatAge(e.FirstTimestamp.Time)
	}
	return components.FormatAge(e.CreationTimestamp.Time)
}

// eventObject formats InvolvedObject as `kind/name`, matching
// `kubectl get events` and the way users reason about which resource
// an event refers to.
func eventObject(e *corev1.Event) string {
	if e.InvolvedObject.Kind == "" {
		return e.InvolvedObject.Name
	}
	return strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Name
}
