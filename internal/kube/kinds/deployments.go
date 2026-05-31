// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Deployments is the plugin for apps/v1 Deployments. Columns mirror
// `kubectl get deployments` so users see the familiar shape.
func Deployments() Plugin {
	return Plugin{
		Key:         "dep",
		DisplayName: "Deployments",
		Kind:        "Deployment",
		GVR:         appsv1.SchemeGroupVersion.WithResource("deployments"),
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "READY", Width: 9},
			{Title: "UP-TO-DATE", Width: 11},
			{Title: "AVAILABLE", Width: 10},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			d, ok := obj.(*appsv1.Deployment)
			if !ok {
				return table.Row{"<malformed>", "", "", "", ""}
			}
			desired := int32(0)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			return table.Row{
				d.Name,
				strconv.Itoa(int(d.Status.ReadyReplicas)) + "/" + strconv.Itoa(int(desired)),
				strconv.Itoa(int(d.Status.UpdatedReplicas)),
				strconv.Itoa(int(d.Status.AvailableReplicas)),
				components.FormatAge(d.CreationTimestamp.Time),
			}
		},
	}
}
