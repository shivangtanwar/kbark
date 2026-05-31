// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Pods is the plugin for pods. Currently unused by the running TUI
// (the legacy typed PodView still drives the pod path so the diagnose
// `?` flow keeps its *corev1.Pod access); kept here so M2.2's PodView
// refactor onto TableResourceView is a deletion-and-rewire, not a
// fresh design pass.
func Pods() Plugin {
	return Plugin{
		Key:         "po",
		DisplayName: "Pods",
		GVR:         corev1.SchemeGroupVersion.WithResource("pods"),
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "READY", Width: 7},
			{Title: "STATUS", Width: 20},
			{Title: "RESTARTS", Width: 10},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			p, ok := obj.(*corev1.Pod)
			if !ok {
				return table.Row{"<malformed>", "", "", "", ""}
			}
			return table.Row{
				p.Name,
				podReady(p),
				podStatus(p),
				podRestarts(p),
				components.FormatAge(p.CreationTimestamp.Time),
			}
		},
	}
}

func podReady(p *corev1.Pod) string {
	total := len(p.Spec.Containers)
	ready := 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

func podRestarts(p *corev1.Pod) string {
	var n int32
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return strconv.FormatInt(int64(n), 10)
}

// podStatus mirrors the heuristic in views/pods.go: prefer a container
// waiting/terminated reason (CrashLoopBackOff, ImagePullBackOff, …)
// over the coarse pod phase, since the reason is what diagnoses care
// about.
func podStatus(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" && cs.State.Terminated.Reason != "Completed" {
			return cs.State.Terminated.Reason
		}
	}
	return string(p.Status.Phase)
}
