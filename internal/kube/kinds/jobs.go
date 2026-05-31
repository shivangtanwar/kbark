// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Jobs is the plugin for batch/v1 Jobs. Columns mirror
// `kubectl get jobs`: name, completions (m/n), duration, age.
func Jobs() Plugin {
	return Plugin{
		Key:         "job",
		DisplayName: "Jobs",
		Kind:        "Job",
		GVR:         batchv1.SchemeGroupVersion.WithResource("jobs"),
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "COMPLETIONS", Width: 12},
			{Title: "DURATION", Width: 10},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			j, ok := obj.(*batchv1.Job)
			if !ok {
				return table.Row{"<malformed>", "", "", ""}
			}
			desired := int32(1)
			if j.Spec.Completions != nil {
				desired = *j.Spec.Completions
			}
			return table.Row{
				j.Name,
				strconv.Itoa(int(j.Status.Succeeded)) + "/" + strconv.Itoa(int(desired)),
				jobDuration(j),
				components.FormatAge(j.CreationTimestamp.Time),
			}
		},
	}
}

// jobDuration is "—" until the job has started, then how long it's
// been running (or, once complete, the total run time). Matches the
// kubectl get jobs output.
func jobDuration(j *batchv1.Job) string {
	if j.Status.StartTime == nil {
		return "—"
	}
	start := j.Status.StartTime.Time
	end := time.Now()
	if j.Status.CompletionTime != nil {
		end = j.Status.CompletionTime.Time
	}
	return components.FormatAgeRelative(start, end)
}
