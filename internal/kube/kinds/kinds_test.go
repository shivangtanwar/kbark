// SPDX-License-Identifier: Apache-2.0

package kinds_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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
		{"configmaps", kinds.ConfigMaps(), "cm", &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Data:       map[string]string{"a": "1", "b": "2"},
		}},
		{"secrets", kinds.Secrets(), "sec", &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"token": []byte("x")},
		}},
		{"ingresses", kinds.Ingresses(), "ing", &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "example.com"}}},
		}},
		{"statefulsets", kinds.StatefulSets(), "sts", &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		}},
		{"daemonsets", kinds.DaemonSets(), "ds", &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		}},
		{"jobs", kinds.Jobs(), "job", &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		}},
		{"cronjobs", kinds.CronJobs(), "cj", &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
			Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *"},
		}},
		{"events", kinds.Events(), "ev", &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "demo"},
			Type:           "Warning",
			Reason:         "Backoff",
			Message:        "Back-off restarting failed container",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "cause-crash"},
		}},
		{"nodes", kinds.Nodes(), "no", &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "demo"},
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
		})
	}
}

// TestPlugins_rowSurvivesWrongType pins the malformed-cast fallback so
// a future plugin author's typo doesn't panic the TUI on a real
// cluster. Each kind's Row must return the same number of cells as
// Columns even when handed an object of the wrong type.
func TestPlugins_rowSurvivesWrongType(t *testing.T) {
	wrong := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}
	allPlugins := []kinds.Plugin{
		kinds.Pods(), kinds.Deployments(), kinds.Services(),
		kinds.ConfigMaps(), kinds.Secrets(), kinds.Ingresses(),
		kinds.StatefulSets(), kinds.DaemonSets(), kinds.Jobs(), kinds.CronJobs(),
		kinds.Events(), kinds.Nodes(),
	}
	for _, p := range allPlugins {
		row := p.Row(wrong)
		if len(row) != len(p.Columns) {
			t.Errorf("%s: Row cells = %d, Columns = %d on wrong-type input", p.Key, len(row), len(p.Columns))
		}
	}
}

// TestSecrets_neverExposesValueContent is a defense-in-depth pin: even
// if a future edit accidentally widens the Row mapper to surface
// secret data, this test catches it before it ships. The full bytes
// of any secret value must NOT appear anywhere in the rendered row.
func TestSecrets_neverExposesValueContent(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token":    []byte("super-secret-token-DO-NOT-LEAK"),
			"password": []byte("hunter2"),
		},
	}
	row := kinds.Secrets().Row(secret)
	for _, cell := range row {
		for _, leak := range []string{"super-secret-token-DO-NOT-LEAK", "hunter2"} {
			if cell == leak {
				t.Fatalf("secret value %q leaked into row cell %q", leak, cell)
			}
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

// TestNodes_isClusterScoped pins the Scope field — Nodes must be
// Cluster so the ResourceService bypasses the user's active namespace.
// Every other shipped kind stays Namespaced (default).
func TestNodes_isClusterScoped(t *testing.T) {
	if kinds.Nodes().Scope != kinds.Cluster {
		t.Errorf("Nodes().Scope = %v, want kinds.Cluster", kinds.Nodes().Scope)
	}
	namespaced := []kinds.Plugin{
		kinds.Pods(), kinds.Deployments(), kinds.Services(),
		kinds.ConfigMaps(), kinds.Secrets(), kinds.Ingresses(),
		kinds.StatefulSets(), kinds.DaemonSets(), kinds.Jobs(), kinds.CronJobs(),
		kinds.Events(),
	}
	for _, p := range namespaced {
		if p.Scope != kinds.Namespaced {
			t.Errorf("%s.Scope = %v, want kinds.Namespaced", p.Key, p.Scope)
		}
	}
}
