// SPDX-License-Identifier: Apache-2.0

// Package kinds describes the resource kinds kbark knows about: their
// short cmdbar keys (`po`, `dep`, `svc`, …), their table columns, and
// how to render one typed object as a table row. Each new kind is one
// file in this package — see deployments.go for the canonical shape.
package kinds

import (
	"github.com/charmbracelet/bubbles/table"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Plugin is the per-kind plug-in surface. Everything kbark needs to
// list, render, and route a resource kind is captured here.
//
// The generic informer is keyed on GVR — `factory.ForResource(gvr)`
// returns the same SharedIndexInformer instance as the typed
// `factory.Apps().V1().Deployments()` accessor, so there's no cache
// duplication, and per-kind plugins stay free of factory-accessor
// boilerplate. The Row mapper is the one place that does the typed
// cast back from runtime.Object to the kind's concrete type.
type Plugin struct {
	// Key is the cmdbar shortcut (e.g. "dep" for `:dep`). Unique per registry.
	Key string
	// DisplayName is the human-facing label ("Deployments").
	DisplayName string
	// GVR identifies the resource for informer/lister access.
	GVR schema.GroupVersionResource
	// Columns are the table headers in the order Row produces them.
	Columns []table.Column
	// Row maps one cached object into a table row. Must produce exactly
	// len(Columns) cells. The first cell is always the resource name (the
	// view's SelectedObject uses it to look up the typed object).
	Row func(runtime.Object) table.Row
}

// Registry holds the plugins kbark was built with, indexed for cmdbar
// dispatch and iteration. Insertion order is preserved so the help text
// can list kinds in a stable, intentional order.
type Registry struct {
	byKey map[string]Plugin
	order []string
}

// NewRegistry builds a registry from the given plugins. Later plugins
// with the same Key overwrite earlier ones; order is preserved from
// first insertion.
func NewRegistry(plugins ...Plugin) *Registry {
	r := &Registry{byKey: make(map[string]Plugin, len(plugins))}
	for _, p := range plugins {
		if _, exists := r.byKey[p.Key]; !exists {
			r.order = append(r.order, p.Key)
		}
		r.byKey[p.Key] = p
	}
	return r
}

// Lookup returns the plugin for `key`, or (zero, false) if no kind
// matches.
func (r *Registry) Lookup(key string) (Plugin, bool) {
	p, ok := r.byKey[key]
	return p, ok
}

// Keys returns plugin keys in insertion order.
func (r *Registry) Keys() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}
