// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// PodService owns the lifecycle of pod informers across namespace switches.
// Each call to Switch tears down the previous lister (if any) and starts a
// fresh one scoped to the new namespace. The returned `done` channel closes
// when a subsequent Switch happens, so consumers (e.g. bubbletea Cmds
// blocked on the snapshots channel) can unblock cleanly without leaking
// goroutines per switch.
type PodService struct {
	client kubernetes.Interface
	resync time.Duration
	parent context.Context

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewPodService builds a PodService bound to a parent context. When parent
// is cancelled the in-flight lister stops too (via context propagation)
// but the current `done` channel is *not* auto-closed — the program is
// shutting down and bubbletea Cmds will be abandoned by Program.Run exit.
func NewPodService(client kubernetes.Interface, resync time.Duration, parent context.Context) *PodService {
	return &PodService{
		client: client,
		resync: resync,
		parent: parent,
	}
}

// Switch tears down the previous lister and starts a new one scoped to
// `namespace`. Returns the snapshots channel, a done channel that closes
// on the next Switch, and any startup error.
//
// Caller should re-listen on the new snapshots channel and select on done
// to release a blocked receive when the next switch happens.
func (s *PodService) Switch(namespace string) (<-chan []*corev1.Pod, <-chan struct{}, error) {
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
	lister := NewPodLister(factory)
	if err := lister.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}

	s.cancel = cancel
	s.done = make(chan struct{})
	return lister.Snapshots(), s.done, nil
}
