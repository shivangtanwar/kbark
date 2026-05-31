// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// StatefulSets is the plugin for apps/v1 StatefulSets. Columns mirror
// `kubectl get statefulsets`: name, ready (m/n), age.
func StatefulSets() Plugin {
	return Plugin{
		Key:         "sts",
		DisplayName: "StatefulSets",
		Kind:        "StatefulSet",
		GVR:         appsv1.SchemeGroupVersion.WithResource("statefulsets"),
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "READY", Width: 9},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			s, ok := obj.(*appsv1.StatefulSet)
			if !ok {
				return table.Row{"<malformed>", "", ""}
			}
			desired := int32(0)
			if s.Spec.Replicas != nil {
				desired = *s.Spec.Replicas
			}
			return table.Row{
				s.Name,
				strconv.Itoa(int(s.Status.ReadyReplicas)) + "/" + strconv.Itoa(int(desired)),
				components.FormatAge(s.CreationTimestamp.Time),
			}
		},
	}
}
