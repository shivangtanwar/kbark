// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newStreamerForTest exposes the LogStreamer internals to tests without
// going through the real StreamPodLogs path (which requires a Kubernetes
// API). The batching logic is independent of where lines come from.
func newStreamerForTest() *LogStreamer {
	return &LogStreamer{
		snapshots: make(chan []string, LogStreamerSnapshotCap),
		errCh:     make(chan error, 1),
		done:      make(chan struct{}),
	}
}

func readBatch(t *testing.T, s *LogStreamer, timeout time.Duration) ([]string, bool) {
	t.Helper()
	select {
	case batch := <-s.Snapshots():
		return batch, true
	case <-time.After(timeout):
		return nil, false
	}
}

func TestLogStreamer_batchesBurstIntoOneSnapshot(t *testing.T) {
	s := newStreamerForTest()
	for i := 0; i < 50; i++ {
		s.append("line-" + strings.Repeat("x", i))
	}

	// Wait at least one debounce window for the timer to fire.
	batch, ok := readBatch(t, s, LogBatchWindow+200*time.Millisecond)
	if !ok {
		t.Fatal("no batch received within debounce + grace")
	}
	if len(batch) != 50 {
		t.Errorf("expected 50 lines in one coalesced batch, got %d", len(batch))
	}

	// Confirm no second batch follows — the burst should not have been
	// split across multiple timer fires.
	if extra, got := readBatch(t, s, 150*time.Millisecond); got {
		t.Errorf("unexpected second batch with %d lines", len(extra))
	}
}

func TestLogStreamer_emitsAcrossWindowBoundaries(t *testing.T) {
	s := newStreamerForTest()
	expected := []string{"first", "second", "third"}
	for _, line := range expected {
		s.append(line)
		// Wait past the debounce window so each line lands in its own batch.
		if _, ok := readBatch(t, s, LogBatchWindow+200*time.Millisecond); !ok {
			t.Fatalf("no batch for %q within debounce + grace", line)
		}
	}
}

func TestLogStreamer_flushDrainsRemainder(t *testing.T) {
	s := newStreamerForTest()
	for i := 0; i < 5; i++ {
		s.append("trailing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// flush should emit whatever's in the buffer even if the debounce
	// timer hasn't fired yet.
	s.flush(ctx)

	select {
	case batch := <-s.Snapshots():
		if len(batch) != 5 {
			t.Errorf("expected flushed batch of 5, got %d", len(batch))
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("flush did not emit a snapshot")
	}
}

func TestLogStreamer_appendAfterCloseIsNoop(t *testing.T) {
	s := newStreamerForTest()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	s.append("should be dropped")

	if batch, ok := readBatch(t, s, 200*time.Millisecond); ok {
		t.Errorf("append after close should be dropped, got %v", batch)
	}
}
