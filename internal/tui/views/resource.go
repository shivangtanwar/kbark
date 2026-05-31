// SPDX-License-Identifier: Apache-2.0

package views

import (
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/kube/kinds"
	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// ResourceView is the kind-generic view the root Model talks to for
// any non-pod resource kind. PodView intentionally does not implement
// this in M2.1 to keep the diagnose `?` flow's typed *corev1.Pod
// access undisturbed — the M2.2 refactor unifies them.
type ResourceView interface {
	SetSize(width, height int) ResourceView
	SetObjects(objs []runtime.Object) ResourceView
	Update(msg tea.Msg) (ResourceView, tea.Cmd)
	View() string
	Kind() string
	// SelectedObject returns the typed object under the cursor, or nil
	// if the table is empty. Future `?`/Enter flows on non-pod kinds
	// (M7, M2.5) get their typed handle from here.
	SelectedObject() runtime.Object
}

// TableResourceView is the only ResourceView implementation kbark
// ships. Every kind plugs its columns + row mapper into the same shell.
type TableResourceView struct {
	plugin  kinds.Plugin
	table   components.Table
	objects []runtime.Object
}

// NewTableResourceView builds a fresh view for the given plugin. The
// table is empty until SetObjects is called.
func NewTableResourceView(th theme.Theme, p kinds.Plugin) TableResourceView {
	return TableResourceView{
		plugin: p,
		table:  components.NewTable(th, p.Columns, nil),
	}
}

func (v TableResourceView) Kind() string { return v.plugin.Key }

func (v TableResourceView) SetSize(width, height int) ResourceView {
	v.table = v.table.SetSize(width, height)
	return v
}

// SetObjects replaces the cached snapshot and re-derives the rows via
// the plugin's mapper. Objects are expected pre-sorted (ResourceLister
// sorts by name).
func (v TableResourceView) SetObjects(objs []runtime.Object) ResourceView {
	v.objects = objs
	rows := make([]table.Row, len(objs))
	for i, o := range objs {
		rows[i] = v.plugin.Row(o)
	}
	v.table = v.table.SetRows(rows)
	return v
}

func (v TableResourceView) Update(msg tea.Msg) (ResourceView, tea.Cmd) {
	var cmd tea.Cmd
	v.table, cmd = v.table.Update(msg)
	return v, cmd
}

func (v TableResourceView) View() string { return v.table.View() }

// SelectedObject matches by the row's first cell (name) — the Plugin
// contract requires Row[0] to be the resource name for exactly this
// reason. Returns nil if the table is empty or the lookup misses
// (shouldn't happen, but doesn't panic if it does).
func (v TableResourceView) SelectedObject() runtime.Object {
	row := v.table.SelectedRow()
	if len(row) == 0 || len(v.objects) == 0 {
		return nil
	}
	name := row[0]
	for _, o := range v.objects {
		a, err := meta.Accessor(o)
		if err != nil {
			continue
		}
		if a.GetName() == name {
			return o
		}
	}
	return nil
}
