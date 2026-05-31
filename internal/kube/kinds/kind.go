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

// Scope is the namespace shape of a resource kind. Most kinds are
// Namespaced — their informer is scoped to one namespace at a time and
// `:ns` re-targets them. Cluster-scoped kinds (nodes, namespaces,
// cluster-roles, …) ignore the active namespace entirely.
type Scope int

const (
	// Namespaced is the default — informer factory is built with
	// WithNamespace(currentNamespace).
	Namespaced Scope = iota
	// Cluster causes the resource service to always build the
	// informer factory without a namespace filter, regardless of the
	// user's active namespace.
	Cluster
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
	// Kind is the canonical singular kind name ("Pod", "Deployment").
	// Together with GVR.GroupVersion() it forms the GVK that
	// kubectl/describe's DescriberFor expects.
	Kind string
	// GVR identifies the resource for informer/lister access.
	GVR schema.GroupVersionResource
	// Scope is Namespaced by default; set to Cluster for kinds like
	// nodes that aren't namespace-bound.
	Scope Scope
	// Columns are the table headers in the order Row produces them.
	Columns []table.Column
	// Row maps one cached object into a table row. Must produce
	// exactly len(Columns) cells. The first cell can be whatever
	// makes sense for the kind — the view's SelectedObject indexes
	// into a parallel objects slice by cursor position, not by
	// matching cell text, so columns are free to be semantic
	// (e.g. events lead with LAST SEEN rather than the synthetic
	// event name).
	Row func(runtime.Object) table.Row
}

// GVK returns the GroupVersionKind derived from GVR's GroupVersion +
// Kind. Used by describe.Service to look up the right kubectl describer.
func (p Plugin) GVK() schema.GroupVersionKind {
	return p.GVR.GroupVersion().WithKind(p.Kind)
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
