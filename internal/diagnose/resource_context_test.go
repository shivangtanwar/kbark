// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shivangtanwar/kbark/internal/diagnose"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// TestResourceContextBuilder_buildIncludesHeader pins the minimum:
// every payload starts with a `## <Kind>: <ns>/<name>` line so the
// model knows what it's reasoning about even when describe fails.
func TestResourceContextBuilder_buildIncludesHeader(t *testing.T) {
	b := diagnose.NewResourceContextBuilder(nil) // nil → header-only path

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "default"},
	}
	out := b.Build(context.Background(), kinds.Deployments(), dep)

	if !strings.Contains(out, "## Deployments: default/nginx") {
		t.Errorf("missing header line, got:\n%s", out)
	}
	if !strings.Contains(out, "describe service unavailable") {
		t.Errorf("nil describe service should explain the limitation, got:\n%s", out)
	}
}

// TestResourceContextBuilder_clusterScopedHeaderOmitsNamespace pins
// the cluster-scoped header variant — a node has no namespace, so
// the "<ns>/<name>" form would produce a leading "/" which reads
// badly to the model.
func TestResourceContextBuilder_clusterScopedHeaderOmitsNamespace(t *testing.T) {
	b := diagnose.NewResourceContextBuilder(nil)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}

	out := b.Build(context.Background(), kinds.Nodes(), node)

	if !strings.Contains(out, "## Nodes: worker-1") {
		t.Errorf("expected cluster-scoped header, got:\n%s", out)
	}
	if !strings.Contains(out, "cluster-scoped") {
		t.Errorf("expected cluster-scoped marker in header, got:\n%s", out)
	}
}

// TestResourceContextBuilder_nilObjectStillProducesHeader guards
// against a panic when the resource view's SelectedObject somehow
// returns nil (race between user pressing `?` and the view clearing).
func TestResourceContextBuilder_nilObjectStillProducesHeader(t *testing.T) {
	b := diagnose.NewResourceContextBuilder(nil)
	out := b.Build(context.Background(), kinds.Deployments(), nil)

	if !strings.Contains(out, "## Deployments:") {
		t.Errorf("nil object should still produce a header line, got:\n%s", out)
	}
}
