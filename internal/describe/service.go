// SPDX-License-Identifier: Apache-2.0

// Package describe produces the human-readable text and YAML
// serialization shown in kbark's Enter modal. Describe text matches
// `kubectl describe` output (events inline, kind-specific formatters)
// via the canonical kubectl/describe package, so the modal feels
// familiar to anyone coming from kubectl.
package describe

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/describe"
	"sigs.k8s.io/yaml"

	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// Service produces describe text and YAML for any kbark-known kind.
//
// Describe uses kubectl/describe under the hood — the same code path
// users see when they run `kubectl describe`. That includes inline
// events, container detail formatting, and the kind-specific layout
// for each resource type.
//
// YAML is computed locally off the cached object, so it returns
// instantly even when the apiserver is sluggish; the modal can show
// YAML immediately and stream in the describe text when it's ready.
type Service struct {
	getter genericclioptions.RESTClientGetter
}

// NewService wraps a RESTClientGetter (e.g. ConfigFlags) for use as
// kubectl/describe's API entry point. May be nil if the caller wants
// YAML-only operation (apiserver unreachable); Describe will then
// return a friendly error.
func NewService(getter genericclioptions.RESTClientGetter) *Service {
	return &Service{getter: getter}
}

// Describe runs the kubectl describer for the plugin's kind and
// returns formatted text. Blocking — caller should run it off the
// main loop. The context bound is advisory; kubectl/describe doesn't
// honour ctx internally (it does its own REST calls), so cancellation
// just abandons the result.
func (s *Service) Describe(ctx context.Context, plugin kinds.Plugin, namespace, name string) (string, error) {
	if s.getter == nil {
		return "", fmt.Errorf("describe service: no REST client configured")
	}
	mapping := mappingFor(plugin)
	describer, err := describe.Describer(s.getter, mapping)
	if err != nil {
		return "", fmt.Errorf("describer for %s: %w", plugin.Kind, err)
	}

	// kubectl/describe is fully synchronous. Run it in a goroutine so
	// caller's ctx can short-circuit a hung apiserver, even though we
	// can't actually abort the in-flight REST call.
	type result struct {
		text string
		err  error
	}
	out := make(chan result, 1)
	go func() {
		text, err := describer.Describe(namespace, name, describe.DescriberSettings{ShowEvents: true})
		out <- result{text: text, err: err}
	}()
	select {
	case r := <-out:
		return r.text, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// YAML serialises the cached object as `kubectl get -o yaml
// --show-managed-fields=false` would. The cached object's TypeMeta
// is typically empty (informers strip it); we copy the object so we
// don't mutate the shared cache, stamp apiVersion + kind from the
// plugin, and strip managedFields — that field is essential for the
// Kubernetes apiserver but pure noise for a human reading the YAML
// (often 100+ lines per object on resources that have been edited
// by multiple controllers).
func (s *Service) YAML(obj runtime.Object, plugin kinds.Plugin) (string, error) {
	if obj == nil {
		return "", fmt.Errorf("yaml: nil object")
	}
	cp := obj.DeepCopyObject()
	cp.GetObjectKind().SetGroupVersionKind(plugin.GVK())
	if accessor, err := meta.Accessor(cp); err == nil {
		accessor.SetManagedFields(nil)
	}
	bytes, err := yaml.Marshal(cp)
	if err != nil {
		return "", fmt.Errorf("yaml marshal: %w", err)
	}
	return string(bytes), nil
}

// mappingFor builds a static RESTMapping from a plugin's GVR/GVK + Scope.
// We bypass discovery (which would round-trip the API for kind data we
// already know) and hand kubectl/describe a fully-formed mapping.
func mappingFor(p kinds.Plugin) *meta.RESTMapping {
	scope := meta.RESTScopeNamespace
	if p.Scope == kinds.Cluster {
		scope = meta.RESTScopeRoot
	}
	return &meta.RESTMapping{
		Resource:         p.GVR,
		GroupVersionKind: p.GVK(),
		Scope:            scope,
	}
}
