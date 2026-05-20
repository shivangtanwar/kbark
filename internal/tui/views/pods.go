// SPDX-License-Identifier: Apache-2.0

// Package views holds the resource-specific TUI views. Each view owns
// a table (or other surface) and knows how to map its resource kind to
// rows. The root Model embeds whichever view is currently active.
package views

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"

	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// PodView renders the pod list table. It owns the table widget and the
// current snapshot; the parent Model feeds it pods via SetPods and
// forwards key events via Update.
type PodView struct {
	table components.Table
	pods  []*corev1.Pod
	th    theme.Theme
}

func NewPodView(th theme.Theme) PodView {
	columns := []table.Column{
		{Title: "NAME", Width: 40},
		{Title: "READY", Width: 7},
		{Title: "STATUS", Width: 20},
		{Title: "RESTARTS", Width: 10},
		{Title: "AGE", Width: 8},
	}
	return PodView{
		table: components.NewTable(th, columns, nil),
		th:    th,
	}
}

func (v PodView) SetSize(width, height int) PodView {
	v.table = v.table.SetSize(width, height)
	return v
}

// SetPods replaces the current snapshot. Pods are expected pre-sorted
// (the informer's PodLister sorts by name).
func (v PodView) SetPods(pods []*corev1.Pod) PodView {
	v.pods = pods
	rows := make([]table.Row, len(pods))
	for i, p := range pods {
		rows[i] = table.Row{
			p.Name,
			readyCount(p),
			deriveStatus(p),
			restartCount(p),
			components.FormatAge(p.CreationTimestamp.Time),
		}
	}
	v.table = v.table.SetRows(rows)
	return v
}

func (v PodView) Update(msg tea.Msg) (PodView, tea.Cmd) {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return v, cmd
}

func (v PodView) View() string {
	return v.table.View()
}

// SelectedPod returns the pod the cursor is currently on, or nil if
// the table is empty / the cursor has somehow drifted out of range.
func (v PodView) SelectedPod() *corev1.Pod {
	row := v.table.SelectedRow()
	if len(row) == 0 || len(v.pods) == 0 {
		return nil
	}
	for _, p := range v.pods {
		if p.Name == row[0] {
			return p
		}
	}
	return nil
}

// readyCount returns "ready/total" containers, mirroring kubectl get pods.
func readyCount(pod *corev1.Pod) string {
	total := len(pod.Spec.Containers)
	ready := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

// restartCount sums restart counts across all containers.
func restartCount(pod *corev1.Pod) string {
	var n int32
	for _, cs := range pod.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return strconv.FormatInt(int64(n), 10)
}

// deriveStatus prefers a container waiting/terminated reason (the
// k8s-specific failure mode like CrashLoopBackOff or ImagePullBackOff)
// over the coarse pod phase. Full parity with kubectl's status logic
// (init containers, deletion timestamps, etc.) lands later.
func deriveStatus(pod *corev1.Pod) string {
	if pod.DeletionTimestamp != nil {
		return "Terminating"
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" && cs.State.Terminated.Reason != "Completed" {
			return cs.State.Terminated.Reason
		}
	}
	return string(pod.Status.Phase)
}
