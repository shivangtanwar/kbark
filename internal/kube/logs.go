// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// LogBatchWindow is the interval over which incoming log lines are coalesced
// before emission. Bursts (10k lines/sec is the design target) flatten into
// ~10 snapshots/sec, which is fast enough to feel live and slow enough that
// the view's viewport.SetContent doesn't dominate the render budget.
const LogBatchWindow = 100 * time.Millisecond

// LogStreamerSnapshotCap bounds how many un-consumed batches we'll buffer
// before applying backpressure to the upstream log read. When the cap is
// reached, new batches are re-buffered and retried on the next tick — slow
// consumer slows the source, no log lines are dropped.
const LogStreamerSnapshotCap = 10

// LogOptions mirror the kubectl log knobs we expose in v1.
type LogOptions struct {
	// Container picks one container in a multi-container pod. Empty means
	// the default-container (server-side rule), matching `kubectl logs`.
	Container string
	// Follow keeps the stream open and emits as new lines appear.
	Follow bool
	// Previous reads logs from the last terminated container instance —
	// the right thing for CrashLoopBackOff diagnosis.
	Previous bool
	// TailLines starts the stream at the last N lines instead of from t=0.
	// Zero or negative means stream from the beginning.
	TailLines int64
}

// LogStreamer batches log lines from a single pod/container into snapshots
// emitted on a channel. Each snapshot is the set of lines received since
// the previous emit — consumers should append, not replace.
//
// Lifecycle: the snapshots channel is never closed (closing it would race
// with in-flight emit goroutines spawned by the debounce timer). Consumers
// detect end-of-stream by selecting on Done() instead.
type LogStreamer struct {
	snapshots chan []string
	errCh     chan error
	done      chan struct{}

	mu       sync.Mutex
	closed   bool
	buffer   []string
	debounce *time.Timer
}

// Snapshots returns the receive-only line-batch channel.
func (s *LogStreamer) Snapshots() <-chan []string { return s.snapshots }

// Errors returns a buffered channel that carries the first non-EOF error
// from the stream (network blip, decode failure, …), if any. Closed
// alongside Done().
func (s *LogStreamer) Errors() <-chan error { return s.errCh }

// Done closes when the upstream reader returns EOF or the context is
// cancelled. Consumers should select on this alongside Snapshots() so
// their Cmd unblocks cleanly when the stream ends.
func (s *LogStreamer) Done() <-chan struct{} { return s.done }

// StreamPodLogs opens the API stream for the given pod/container and
// starts the batching goroutine. Returns immediately; the goroutine
// terminates when ctx is cancelled or the stream EOFs.
func StreamPodLogs(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, pod, container string,
	opts LogOptions,
) (*LogStreamer, error) {
	podLogOpts := &corev1.PodLogOptions{
		Container: container,
		Follow:    opts.Follow,
		Previous:  opts.Previous,
	}
	if opts.TailLines > 0 {
		podLogOpts.TailLines = &opts.TailLines
	}

	req := client.CoreV1().Pods(namespace).GetLogs(pod, podLogOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open log stream for %s/%s: %w", namespace, pod, err)
	}

	s := &LogStreamer{
		snapshots: make(chan []string, LogStreamerSnapshotCap),
		errCh:     make(chan error, 1),
		done:      make(chan struct{}),
	}
	go s.run(ctx, stream)
	return s, nil
}

func (s *LogStreamer) run(ctx context.Context, r io.ReadCloser) {
	defer func() {
		_ = r.Close()
		s.flush(ctx)
		s.mu.Lock()
		s.closed = true
		if s.debounce != nil {
			s.debounce.Stop()
			s.debounce = nil
		}
		s.mu.Unlock()
		close(s.done)
		close(s.errCh)
		// snapshots is intentionally left open; in-flight timerFire
		// goroutines may still try to send and the checks under s.mu
		// ensure they don't double-emit after closed=true.
	}()

	scanner := bufio.NewScanner(r)
	// Some log lines are huge (stack traces, json dumps). Allow up to 1 MiB
	// per line before bufio gives up.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		s.append(scanner.Text())
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		select {
		case s.errCh <- err:
		default:
		}
	}
}

func (s *LogStreamer) append(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.buffer = append(s.buffer, line)
	if s.debounce == nil {
		s.debounce = time.AfterFunc(LogBatchWindow, s.timerFire)
		return
	}
	s.debounce.Reset(LogBatchWindow)
}

func (s *LogStreamer) timerFire() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return
	}
	snapshot := s.buffer
	s.buffer = nil
	s.mu.Unlock()

	select {
	case s.snapshots <- snapshot:
	default:
		// Channel full. Re-buffer for the next tick. If we got closed
		// between unlock and here, the closed check guards re-buffer.
		s.mu.Lock()
		if !s.closed {
			s.buffer = append(snapshot, s.buffer...)
		}
		s.mu.Unlock()
	}
}

// flush emits any pending lines, respecting ctx so we don't block on a
// consumer that's already abandoning us.
func (s *LogStreamer) flush(ctx context.Context) {
	s.mu.Lock()
	snapshot := s.buffer
	s.buffer = nil
	if s.debounce != nil {
		s.debounce.Stop()
		s.debounce = nil
	}
	s.mu.Unlock()
	if len(snapshot) == 0 {
		return
	}
	select {
	case s.snapshots <- snapshot:
	case <-ctx.Done():
	}
}
