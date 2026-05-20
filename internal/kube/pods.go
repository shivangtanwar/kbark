// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// DefaultDebounce is the window we coalesce informer events over before
// emitting a snapshot. Tuned to absorb the initial-sync burst (a hundred
// pods on a fresh cluster arrive in milliseconds) into a single render.
const DefaultDebounce = 100 * time.Millisecond

// PodLister observes the Pod informer and pushes sorted snapshots of the
// current pod set onto a channel. Snapshots are coalesced over DefaultDebounce
// so the TUI re-renders once per burst, not once per event.
//
// The channel is buffered to 1: if the consumer is slow, the previous
// snapshot is dropped in favour of the latest. The TUI only ever cares
// about the current state, never about intermediate ones.
type PodLister struct {
	factory  informers.SharedInformerFactory
	lister   corev1listers.PodLister
	snapshot chan []*corev1.Pod

	mu       sync.Mutex
	debounce *time.Timer
	window   time.Duration
}

// NewPodLister attaches the lister to the factory's pod informer and
// registers event handlers. Call Start to actually run the informer.
func NewPodLister(factory informers.SharedInformerFactory) *PodLister {
	podInformer := factory.Core().V1().Pods()
	pl := &PodLister{
		factory:  factory,
		lister:   podInformer.Lister(),
		snapshot: make(chan []*corev1.Pod, 1),
		window:   DefaultDebounce,
	}
	_, _ = podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { pl.bump() },
		UpdateFunc: func(_, _ interface{}) { pl.bump() },
		DeleteFunc: func(_ interface{}) { pl.bump() },
	})
	return pl
}

// Start runs the factory and blocks until the pod informer's cache is
// synced. After Start returns, the consumer should expect a snapshot on
// Snapshots() within `window` even on an empty cluster (the initial
// "list complete" tick still bumps the timer).
func (pl *PodLister) Start(ctx context.Context) error {
	pl.factory.Start(ctx.Done())
	synced := pl.factory.WaitForCacheSync(ctx.Done())
	for typ, ok := range synced {
		if !ok {
			return fmt.Errorf("informer %v did not sync before context cancelled", typ)
		}
	}
	// Force a first snapshot even if there were zero events during sync.
	pl.bump()
	return nil
}

// Snapshots returns a receive-only channel of pod slices, sorted by name.
// Each receive consumes the latest available snapshot; if a snapshot
// arrives while the previous one is still pending in the buffer, the
// pending one is dropped.
func (pl *PodLister) Snapshots() <-chan []*corev1.Pod {
	return pl.snapshot
}

func (pl *PodLister) bump() {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.debounce == nil {
		pl.debounce = time.AfterFunc(pl.window, pl.emit)
		return
	}
	pl.debounce.Reset(pl.window)
}

func (pl *PodLister) emit() {
	pods, err := pl.lister.List(labels.Everything())
	if err != nil {
		// Local cache list is documented as never erroring. Skip on the
		// off chance the contract changes — better to drop a frame than
		// crash the program.
		return
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })

	select {
	case pl.snapshot <- pods:
	default:
		// Buffer full; drop the stale snapshot and put the fresh one.
		select {
		case <-pl.snapshot:
		default:
		}
		select {
		case pl.snapshot <- pods:
		default:
		}
	}
}
