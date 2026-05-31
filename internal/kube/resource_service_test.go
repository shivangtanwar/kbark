// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

func TestResourceService_switchReplacesChannel(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := kube.NewResourceService(cs, kube.DefaultResyncInterval, ctx, kinds.Deployments())
	ch1, done1, err := svc.Switch("ns-a")
	if err != nil {
		t.Fatalf("first Switch: %v", err)
	}
	ch2, _, err := svc.Switch("ns-b")
	if err != nil {
		t.Fatalf("second Switch: %v", err)
	}

	if ch1 == ch2 {
		t.Fatal("Switch returned the same snapshots channel twice")
	}

	select {
	case <-done1:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("done1 not closed after second Switch")
	}
}

func TestResourceService_noGoroutineLeakAcross50Switches(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := kube.NewResourceService(cs, kube.DefaultResyncInterval, ctx, kinds.Services())

	if _, _, err := svc.Switch("warmup"); err != nil {
		t.Fatalf("warmup Switch: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		if _, _, err := svc.Switch(fmt.Sprintf("ns-%d", i)); err != nil {
			t.Fatalf("Switch #%d: %v", i, err)
		}
	}

	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	final := runtime.NumGoroutine()

	const tolerance = 5
	if final > baseline+tolerance {
		t.Errorf("goroutine count grew from %d to %d after 50 switches (tolerance %d)",
			baseline, final, tolerance)
	}
}

func TestResourceService_kindAccessor(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx := context.Background()
	svc := kube.NewResourceService(cs, kube.DefaultResyncInterval, ctx, kinds.Deployments())
	if got := svc.Kind(); got != "dep" {
		t.Errorf("Kind() = %q, want %q", got, "dep")
	}
}
