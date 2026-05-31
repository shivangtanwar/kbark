// SPDX-License-Identifier: Apache-2.0

package kinds_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// pluginShape is the minimum every Plugin must satisfy. Catches the
// off-by-one between Columns and Row cells that's the most likely
// regression as M2.2+ adds 8 more kinds.
type pluginShape struct {
	name    string
	plugin  kinds.Plugin
	wantKey string
	fixture runtime.Object
}

func TestPlugins_haveValidShape(t *testing.T) {
	cases := []pluginShape{
		{"pods", kinds.Pods(), "po", &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
		}},
		{"deployments", kinds.Deployments(), "dep", &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		}},
		{"services", kinds.Services(), "svc", &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1",
				Ports: []corev1.ServicePort{{Port: 80, Protocol: "TCP"}},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", tc.plugin.Key, tc.wantKey)
			}
			if tc.plugin.DisplayName == "" {
				t.Error("DisplayName empty")
			}
			if tc.plugin.GVR.Empty() {
				t.Error("GVR empty")
			}
			if len(tc.plugin.Columns) == 0 {
				t.Error("Columns empty")
			}
			if tc.plugin.Row == nil {
				t.Fatal("Row nil")
			}
			row := tc.plugin.Row(tc.fixture)
			if len(row) != len(tc.plugin.Columns) {
				t.Errorf("Row cells = %d, Columns = %d (must match)", len(row), len(tc.plugin.Columns))
			}
			if row[0] != "demo" {
				t.Errorf("Row[0] = %q, want %q (first cell must be name so SelectedObject can match)", row[0], "demo")
			}
		})
	}
}

// TestPlugins_rowSurvivesWrongType pins the malformed-cast fallback so
// a future plugin author's typo doesn't panic the TUI on a real
// cluster. Each kind's Row must return the same number of cells as
// Columns even when handed an object of the wrong type.
func TestPlugins_rowSurvivesWrongType(t *testing.T) {
	wrong := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}
	for _, p := range []kinds.Plugin{kinds.Pods(), kinds.Deployments(), kinds.Services()} {
		row := p.Row(wrong)
		if len(row) != len(p.Columns) {
			t.Errorf("%s: Row cells = %d, Columns = %d on wrong-type input", p.Key, len(row), len(p.Columns))
		}
	}
}

func TestRegistry_lookupAndKeys(t *testing.T) {
	r := kinds.NewRegistry(kinds.Pods(), kinds.Deployments(), kinds.Services())

	if got := r.Keys(); len(got) != 3 || got[0] != "po" || got[1] != "dep" || got[2] != "svc" {
		t.Errorf("Keys() = %v, want [po dep svc] in insertion order", got)
	}
	if _, ok := r.Lookup("dep"); !ok {
		t.Error(`Lookup("dep") = false, want true`)
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Error(`Lookup("nope") = true, want false`)
	}
}
