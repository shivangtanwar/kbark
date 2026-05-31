// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// DaemonSets is the plugin for apps/v1 DaemonSets. Columns mirror
// `kubectl get daemonsets` minus NODE SELECTOR (rarely the key info
// for diagnosis, and rotation of selector keys would balloon the cell).
func DaemonSets() Plugin {
	return Plugin{
		Key:         "ds",
		DisplayName: "DaemonSets",
		Kind:        "DaemonSet",
		GVR:         appsv1.SchemeGroupVersion.WithResource("daemonsets"),
		Columns: []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "DESIRED", Width: 8},
			{Title: "CURRENT", Width: 8},
			{Title: "READY", Width: 6},
			{Title: "UP-TO-DATE", Width: 11},
			{Title: "AVAILABLE", Width: 10},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			d, ok := obj.(*appsv1.DaemonSet)
			if !ok {
				return table.Row{"<malformed>", "", "", "", "", "", ""}
			}
			return table.Row{
				d.Name,
				strconv.Itoa(int(d.Status.DesiredNumberScheduled)),
				strconv.Itoa(int(d.Status.CurrentNumberScheduled)),
				strconv.Itoa(int(d.Status.NumberReady)),
				strconv.Itoa(int(d.Status.UpdatedNumberScheduled)),
				strconv.Itoa(int(d.Status.NumberAvailable)),
				components.FormatAge(d.CreationTimestamp.Time),
			}
		},
	}
}
