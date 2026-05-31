// SPDX-License-Identifier: Apache-2.0

package views_test

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/kube/kinds"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

func newDep(name string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func TestTableResourceView_setObjectsRendersRows(t *testing.T) {
	v := views.NewTableResourceView(theme.Default(), kinds.Deployments())
	rv := v.SetSize(120, 24).
		SetObjects([]runtime.Object{newDep("alpha"), newDep("beta")})

	view := rv.View()
	for _, want := range []string{"alpha", "beta", "NAME"} {
		if !strings.Contains(view, want) {
			t.Errorf("view should contain %q, got:\n%s", want, view)
		}
	}
}

func TestTableResourceView_selectedObjectMatchesByName(t *testing.T) {
	v := views.NewTableResourceView(theme.Default(), kinds.Deployments())
	rv := v.SetSize(120, 24).
		SetObjects([]runtime.Object{newDep("alpha"), newDep("beta")})

	got := rv.SelectedObject()
	if got == nil {
		t.Fatal("SelectedObject() = nil; want first row's deployment")
	}
	dep, ok := got.(*appsv1.Deployment)
	if !ok {
		t.Fatalf("SelectedObject() type = %T, want *appsv1.Deployment", got)
	}
	if dep.Name != "alpha" {
		t.Errorf("SelectedObject().Name = %q, want %q", dep.Name, "alpha")
	}
}

func TestTableResourceView_selectedObjectEmpty(t *testing.T) {
	v := views.NewTableResourceView(theme.Default(), kinds.Deployments())
	rv := v.SetSize(120, 24)
	if got := rv.SelectedObject(); got != nil {
		t.Errorf("SelectedObject on empty view = %v, want nil", got)
	}
}

func TestTableResourceView_kindReturnsPluginKey(t *testing.T) {
	v := views.NewTableResourceView(theme.Default(), kinds.Services())
	if got := v.Kind(); got != "svc" {
		t.Errorf("Kind() = %q, want %q", got, "svc")
	}
}
