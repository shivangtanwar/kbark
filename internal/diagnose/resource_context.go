// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/describe"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// ResourceContextBuilder assembles the AI payload for `?` on a
// non-pod kind. The bulk of the payload is kubectl-style describe
// output (which already inlines events and kind-specific structure),
// wrapped with a small header that names the kind, namespace, and
// resource. Pods keep their dedicated PodContextBuilder because they
// also need a log tail that describe doesn't surface.
type ResourceContextBuilder struct {
	describeService *describe.Service
}

// NewResourceContextBuilder wraps an existing describe.Service. The
// service may be nil when REST config wiring failed at startup — the
// builder then produces a header-only payload and the model surfaces
// the limitation in its answer.
func NewResourceContextBuilder(d *describe.Service) *ResourceContextBuilder {
	return &ResourceContextBuilder{describeService: d}
}

// Build returns the payload for a single non-pod resource. Best-effort:
// a describe failure (apiserver hiccup, RBAC) falls back to a header
// + an inline error note rather than aborting the diagnosis.
func (b *ResourceContextBuilder) Build(ctx context.Context, plugin kinds.Plugin, obj runtime.Object) string {
	namespace, name := metaIdentity(obj)

	var out strings.Builder
	if namespace != "" {
		fmt.Fprintf(&out, "## %s: %s/%s\n\n", plugin.DisplayName, namespace, name)
	} else {
		fmt.Fprintf(&out, "## %s: %s (cluster-scoped)\n\n", plugin.DisplayName, name)
	}

	if b.describeService == nil {
		out.WriteString("(describe service unavailable; only the resource header is included)\n")
		return out.String()
	}

	text, err := b.describeService.Describe(ctx, plugin, namespace, name)
	if err != nil {
		fmt.Fprintf(&out, "(describe failed: %v)\n", err)
		return out.String()
	}

	out.WriteString("### Describe (kubectl-style, events inline)\n")
	out.WriteString("```\n")
	out.WriteString(strings.TrimRight(text, "\n"))
	out.WriteString("\n```\n")

	return out.String()
}

func metaIdentity(obj runtime.Object) (namespace, name string) {
	if obj == nil {
		return "", ""
	}
	if a, err := meta.Accessor(obj); err == nil && a != nil {
		return a.GetNamespace(), a.GetName()
	}
	return "", ""
}
