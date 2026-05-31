// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Secrets is the plugin for core/v1 Secrets. The DATA column is the
// count of keys — never the values. M2 deliberately exposes no Secret
// value content anywhere in the TUI; M8 will add a redaction-aware
// reveal flow gated on profile/policy. Until then `kbark` shows
// exactly what `kubectl get secrets` does and nothing more.
func Secrets() Plugin {
	return Plugin{
		Key:         "sec",
		DisplayName: "Secrets",
		GVR:         corev1.SchemeGroupVersion.WithResource("secrets"),
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "TYPE", Width: 30},
			{Title: "DATA", Width: 6},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			s, ok := obj.(*corev1.Secret)
			if !ok {
				return table.Row{"<malformed>", "", "", ""}
			}
			return table.Row{
				s.Name,
				string(s.Type),
				strconv.Itoa(len(s.Data)),
				components.FormatAge(s.CreationTimestamp.Time),
			}
		},
	}
}
