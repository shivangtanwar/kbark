// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// ResourceLister is the kind-generic informer wrapper. Behaviour:
// debounce events over DefaultDebounce, sort by name, emit on a
// buffer-1 channel that drops stale snapshots when the consumer falls
// behind, and bound startup sync on SyncTimeout so an unreachable
// apiserver fails fast.
//
// The per-kind plug-in surface is just Plugin.GVR — `factory.ForResource`
// hands back the same SharedIndexInformer instance that the typed
// accessors would, so there's no cache duplication.
type ResourceLister struct {
	factory  informers.SharedInformerFactory
	plugin   kinds.Plugin
	informer cache.SharedIndexInformer
	lister   cache.GenericLister
	snapshot chan []runtime.Object

	mu       sync.Mutex
	debounce *time.Timer
	window   time.Duration
}

// NewResourceLister attaches event handlers to the kind's informer and
// returns a lister ready to Start. The factory itself is not started
// yet — caller controls that via Start, so multiple kinds can share one
// factory if needed.
func NewResourceLister(factory informers.SharedInformerFactory, plugin kinds.Plugin) (*ResourceLister, error) {
	ginf, err := factory.ForResource(plugin.GVR)
	if err != nil {
		return nil, fmt.Errorf("resource lister: factory.ForResource(%s): %w", plugin.GVR, err)
	}
	rl := &ResourceLister{
		factory:  factory,
		plugin:   plugin,
		informer: ginf.Informer(),
		lister:   ginf.Lister(),
		snapshot: make(chan []runtime.Object, 1),
		window:   DefaultDebounce,
	}
	_, _ = rl.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { rl.bump() },
		UpdateFunc: func(_, _ interface{}) { rl.bump() },
		DeleteFunc: func(_ interface{}) { rl.bump() },
	})
	return rl, nil
}

// Start runs the factory and blocks until this kind's informer cache
// is synced — or SyncTimeout elapses.
func (rl *ResourceLister) Start(ctx context.Context) error {
	rl.factory.Start(ctx.Done())

	syncCtx, cancel := context.WithTimeout(ctx, SyncTimeout)
	defer cancel()

	synced := rl.factory.WaitForCacheSync(syncCtx.Done())
	for typ, ok := range synced {
		if !ok {
			if ctx.Err() != nil {
				return fmt.Errorf("informer %v sync interrupted: %w", typ, ctx.Err())
			}
			return fmt.Errorf("informer %v did not sync within %s (apiserver unreachable?)", typ, SyncTimeout)
		}
	}
	// Force a first snapshot even if there were zero events during sync.
	rl.bump()
	return nil
}

// Snapshots returns a receive-only channel of object slices, sorted by
// name. Buffered to 1; stale snapshots get dropped in favour of fresh.
func (rl *ResourceLister) Snapshots() <-chan []runtime.Object {
	return rl.snapshot
}

func (rl *ResourceLister) bump() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.debounce == nil {
		rl.debounce = time.AfterFunc(rl.window, rl.emit)
		return
	}
	rl.debounce.Reset(rl.window)
}

func (rl *ResourceLister) emit() {
	objs, err := rl.lister.List(labels.Everything())
	if err != nil {
		// Cache list is documented as non-failing; drop the frame on the
		// off chance the contract changes rather than crashing the TUI.
		return
	}
	sort.Slice(objs, func(i, j int) bool {
		// meta.Accessor cannot fail on a typed kube object that the
		// informer cache stored. Fall back to "" to keep the sort stable
		// against the impossible-but-possible case.
		ai, _ := meta.Accessor(objs[i])
		aj, _ := meta.Accessor(objs[j])
		var ni, nj string
		if ai != nil {
			ni = ai.GetName()
		}
		if aj != nil {
			nj = aj.GetName()
		}
		return ni < nj
	})

	select {
	case rl.snapshot <- objs:
	default:
		// Drop the stale snapshot in favour of the fresh one.
		select {
		case <-rl.snapshot:
		default:
		}
		select {
		case rl.snapshot <- objs:
		default:
		}
	}
}
