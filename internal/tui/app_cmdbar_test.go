// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/describe"
	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

// modelWithRegistry returns a Model wired with the kind registry and
// a pre-built ResourceService for "dep" backed by a fake clientset.
// Skips the bubbletea program — submitCmd is callable directly because
// the cmdbar can be primed via SetValue.
func modelWithRegistry(t *testing.T) Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cs := fake.NewSimpleClientset()
	registry := kinds.NewRegistry(kinds.Pods(), kinds.Deployments(), kinds.Services())
	services := map[string]*kube.ResourceService{
		"dep": kube.NewResourceService(cs, kube.DefaultResyncInterval, ctx, kinds.Deployments()),
		"svc": kube.NewResourceService(cs, kube.DefaultResyncInterval, ctx, kinds.Services()),
	}

	th := theme.Default()
	return Model{
		ctx:              ctx,
		active:           ViewPods,
		namespace:        "default",
		cmdbar:           components.NewCmdbar(th).Activate(),
		registry:         registry,
		resourceServices: services,
		th:               th,
		width:            120,
		height:           24,
	}
}

// TestSubmitCmd_kindKeyOpensResourceView pins the cmdbar dispatch for
// `:dep` and `:svc` — both must transition into ViewResource and set
// resourceKind to the looked-up plugin key.
func TestSubmitCmd_kindKeyOpensResourceView(t *testing.T) {
	for _, key := range []string{"dep", "svc"} {
		t.Run(key, func(t *testing.T) {
			m := modelWithRegistry(t)
			m.cmdbar = m.cmdbar.SetValue(key)

			next, _ := m.submitCmd()
			if next.active != ViewResource {
				t.Errorf("after :%s, active = %v, want ViewResource", key, next.active)
			}
			if next.resourceKind != key {
				t.Errorf("after :%s, resourceKind = %q, want %q", key, next.resourceKind, key)
			}
			if next.resourceView == nil {
				t.Errorf("after :%s, resourceView is nil", key)
			}
		})
	}
}

// TestSubmitCmd_poReturnsToPods pins the `:po` shortcut that takes
// the user back to the pod view from a resource view.
func TestSubmitCmd_poReturnsToPods(t *testing.T) {
	m := modelWithRegistry(t)
	m.active = ViewResource
	m.resourceKind = "dep"
	m.cmdbar = m.cmdbar.SetValue("po")

	next, _ := m.submitCmd()
	if next.active != ViewPods {
		t.Errorf("after :po, active = %v, want ViewPods", next.active)
	}
}

// TestSubmitCmd_unknownKeySetsError pins the failure path: a typo or
// unknown kind must report inline and leave the active view alone.
func TestSubmitCmd_unknownKeySetsError(t *testing.T) {
	m := modelWithRegistry(t)
	m.cmdbar = m.cmdbar.SetValue("bogus")

	next, _ := m.submitCmd()
	if next.active != ViewPods {
		t.Errorf("after :bogus, active = %v, want ViewPods (unchanged)", next.active)
	}
	if next.resourceKind != "" {
		t.Errorf("after :bogus, resourceKind = %q, want empty", next.resourceKind)
	}
}

// TestOpenDescribeForResource_routesToDescribeView pins the M2.5b
// flip — Enter on a non-pod resource view must open the describe
// modal, set prevActive=ViewResource (so esc returns to the resource
// list), and seed the YAML buffer off the cached object.
func TestOpenDescribeForResource_routesToDescribeView(t *testing.T) {
	m := modelWithRegistry(t)
	m.describeService = describe.NewService(nil)
	m.describeView = views.NewDescribeView(theme.Default())

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"}}
	rv := views.NewTableResourceView(theme.Default(), kinds.Deployments())
	m.resourceView = rv.SetSize(120, 24).SetObjects([]runtime.Object{dep})
	m.resourceKind = "dep"
	m.active = ViewResource

	next, _ := m.openDescribeForResource()
	if next.active != ViewDescribe {
		t.Errorf("active = %v, want ViewDescribe", next.active)
	}
	if next.prevActive != ViewResource {
		t.Errorf("prevActive = %v, want ViewResource (so esc returns to :dep)", next.prevActive)
	}
}

// TestOpenDescribeForResource_emptyViewIsNoop guards against a panic
// when Enter is pressed before the resource view has any rows.
func TestOpenDescribeForResource_emptyViewIsNoop(t *testing.T) {
	m := modelWithRegistry(t)
	m.describeService = describe.NewService(nil)
	m.describeView = views.NewDescribeView(theme.Default())
	rv := views.NewTableResourceView(theme.Default(), kinds.Deployments())
	m.resourceView = rv.SetSize(120, 24).SetObjects(nil)
	m.resourceKind = "dep"
	m.active = ViewResource

	next, cmd := m.openDescribeForResource()
	if next.active != ViewResource {
		t.Errorf("empty Enter should leave active=ViewResource, got %v", next.active)
	}
	if cmd != nil {
		t.Errorf("empty Enter should return nil Cmd, got %v", cmd)
	}
}

// TestSubmitCmd_nsCommandStillWorks is a regression guard — adding
// `:<kind>` parsing must not break `:ns <namespace>`.
func TestSubmitCmd_nsCommandStillWorks(t *testing.T) {
	m := modelWithRegistry(t)
	m.cmdbar = m.cmdbar.SetValue("ns kube-system")

	_, cmd := m.submitCmd()
	if cmd == nil {
		t.Fatal("submitCmd(:ns kube-system) returned nil Cmd; expected a NamespaceChangedMsg producer")
	}
	msg := cmd()
	got, ok := msg.(NamespaceChangedMsg)
	if !ok {
		t.Fatalf("Cmd produced %T, want NamespaceChangedMsg", msg)
	}
	if got.Namespace != "kube-system" {
		t.Errorf("NamespaceChangedMsg.Namespace = %q, want %q", got.Namespace, "kube-system")
	}
}
