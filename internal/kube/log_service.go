// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"sync"

	"k8s.io/client-go/kubernetes"
)

// LogService owns the lifecycle of pod log streamers. Each call to
// Stream cancels the previous streamer (if any) and opens a new one,
// mirroring the cancel-and-replace lifecycle used by ResourceService
// for informers.
//
// Use case: the user opens the logs view on pod A, then navigates to pod B
// and opens logs on B. The streamer for A needs to be torn down so its
// goroutines exit and the upstream API connection closes; LogService does
// that under the hood without the parent Model having to track contexts.
type LogService struct {
	client kubernetes.Interface
	parent context.Context

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewLogService(client kubernetes.Interface, parent context.Context) *LogService {
	return &LogService{
		client: client,
		parent: parent,
	}
}

// Stream cancels any in-flight log streamer and opens a new one against
// the given pod/container with the provided options.
func (s *LogService) Stream(namespace, pod, container string, opts LogOptions) (*LogStreamer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}

	ctx, cancel := context.WithCancel(s.parent)
	streamer, err := StreamPodLogs(ctx, s.client, namespace, pod, container, opts)
	if err != nil {
		cancel()
		return nil, err
	}
	s.cancel = cancel
	return streamer, nil
}

// Stop cancels the current streamer if any. Used when the user leaves the
// logs view without picking a different pod.
func (s *LogService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}
