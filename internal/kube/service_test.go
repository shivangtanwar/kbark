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
)

func TestPodService_switchReplacesChannel(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := kube.NewPodService(cs, kube.DefaultResyncInterval, ctx)
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

	// done1 must be closed after the second Switch so that any waiter
	// blocked on ch1 can unblock via the select-on-done branch.
	select {
	case <-done1:
		// good
	case <-time.After(200 * time.Millisecond):
		t.Fatal("done1 not closed after second Switch")
	}
}

func TestPodService_noGoroutineLeakAcross50Switches(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := kube.NewPodService(cs, kube.DefaultResyncInterval, ctx)

	// Warm-up switch so the baseline reflects steady-state goroutine count.
	if _, _, err := svc.Switch("warmup"); err != nil {
		t.Fatalf("warmup Switch: %v", err)
	}
	// Let the warmup goroutines settle.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		if _, _, err := svc.Switch(fmt.Sprintf("ns-%d", i)); err != nil {
			t.Fatalf("Switch #%d: %v", i, err)
		}
	}

	// Goroutine shutdown is asynchronous (informer stop, watcher close,
	// debounce timer cancel). Give them time to actually exit.
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	final := runtime.NumGoroutine()

	// Allow a small tolerance for runtime jitter and the *current* lister
	// (which legitimately still has goroutines running).
	const tolerance = 5
	if final > baseline+tolerance {
		t.Errorf("goroutine count grew from %d to %d after 50 switches (tolerance %d)",
			baseline, final, tolerance)
	}
}
