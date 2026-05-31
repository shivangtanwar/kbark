// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// ConfigMaps is the plugin for core/v1 ConfigMaps. Columns mirror
// `kubectl get configmaps`: name, data-key count, age. We deliberately
// don't render any key/value content in the table — the user opens
// the (future M2.5) describe modal for that.
func ConfigMaps() Plugin {
	return Plugin{
		Key:         "cm",
		DisplayName: "ConfigMaps",
		GVR:         corev1.SchemeGroupVersion.WithResource("configmaps"),
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "DATA", Width: 6},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			cm, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return table.Row{"<malformed>", "", ""}
			}
			return table.Row{
				cm.Name,
				strconv.Itoa(len(cm.Data) + len(cm.BinaryData)),
				components.FormatAge(cm.CreationTimestamp.Time),
			}
		},
	}
}
