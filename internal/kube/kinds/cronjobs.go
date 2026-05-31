// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// CronJobs is the plugin for batch/v1 CronJobs. Columns mirror
// `kubectl get cronjobs`: name, schedule, suspend, active, last-schedule, age.
func CronJobs() Plugin {
	return Plugin{
		Key:         "cj",
		DisplayName: "CronJobs",
		Kind:        "CronJob",
		GVR:         batchv1.SchemeGroupVersion.WithResource("cronjobs"),
		Columns: []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "SCHEDULE", Width: 16},
			{Title: "SUSPEND", Width: 8},
			{Title: "ACTIVE", Width: 7},
			{Title: "LAST SCHEDULE", Width: 14},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			cj, ok := obj.(*batchv1.CronJob)
			if !ok {
				return table.Row{"<malformed>", "", "", "", "", ""}
			}
			suspend := "False"
			if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
				suspend = "True"
			}
			lastSched := "<never>"
			if cj.Status.LastScheduleTime != nil {
				lastSched = components.FormatAge(cj.Status.LastScheduleTime.Time)
			}
			return table.Row{
				cj.Name,
				cj.Spec.Schedule,
				suspend,
				strconv.Itoa(len(cj.Status.Active)),
				lastSched,
				components.FormatAge(cj.CreationTimestamp.Time),
			}
		},
	}
}
