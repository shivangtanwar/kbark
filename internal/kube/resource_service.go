// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"

	"github.com/shivangtanwar/kbark/internal/kube/kinds"
)

// ResourceService is the kind-generic counterpart of PodService. It owns
// the namespace-switch lifecycle for one kind: each Switch tears down
// the previous lister and starts a fresh one scoped to the new namespace.
// One instance per kind; the TUI keeps a map keyed by Plugin.Key.
type ResourceService struct {
	client kubernetes.Interface
	resync time.Duration
	parent context.Context
	plugin kinds.Plugin

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewResourceService builds a service for the given kind. No informer
// is started until Switch is called, so constructing one per registered
// kind at startup is cheap.
func NewResourceService(client kubernetes.Interface, resync time.Duration, parent context.Context, plugin kinds.Plugin) *ResourceService {
	return &ResourceService{
		client: client,
		resync: resync,
		parent: parent,
		plugin: plugin,
	}
}

// Kind returns the plugin key, useful for routing snapshots back to the
// view that asked for them.
func (s *ResourceService) Kind() string { return s.plugin.Key }

// Switch tears down the previous lister and starts a new one scoped to
// `namespace`. Returns the snapshots channel, a done channel that closes
// on the next Switch, and any startup error. Same contract as
// PodService.Switch.
func (s *ResourceService) Switch(namespace string) (<-chan []runtime.Object, <-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
		close(s.done)
		s.cancel = nil
		s.done = nil
	}

	ctx, cancel := context.WithCancel(s.parent)
	factory := NewFactory(s.client, namespace, s.resync)
	lister, err := NewResourceLister(factory, s.plugin)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := lister.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}

	s.cancel = cancel
	s.done = make(chan struct{})
	return lister.Snapshots(), s.done, nil
}
