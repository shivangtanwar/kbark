// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shivangtanwar/kbark/internal/incident"
)

// historyWindow is how much trend history is kept.
const historyWindow = 24 * time.Hour

// Sample is one point of a cluster's trend line.
type Sample struct {
	T       time.Time `json:"t"`
	Cluster string    `json:"cluster"`
	Healthy int       `json:"healthy"`
	Failing int       `json:"failing"`
	Pending int       `json:"pending"`
	// Open is the number of open incidents in the cluster.
	Open int `json:"open"`
}

// sample records a trend point per connected cluster, at most once per
// SampleEvery.
func (e *Engine) sample(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now.Sub(e.lastSample) < e.cfg.SampleEvery {
		return
	}
	open := map[string]int{}
	for _, inc := range e.store.List() {
		if inc.ClosedAt == nil {
			open[inc.Cluster]++
		}
	}
	recorded := false
	for _, name := range e.order {
		s, ok := e.snaps[name]
		if !ok || s.Summary.State != "connected" {
			continue
		}
		e.samples = append(e.samples, Sample{
			T: now.UTC().Truncate(time.Second), Cluster: name,
			Healthy: s.Summary.Healthy, Failing: s.Summary.Failing, Pending: s.Summary.Pending, Open: open[name],
		})
		recorded = true
	}
	if !recorded {
		return
	}
	e.lastSample = now
	cut := now.Add(-historyWindow)
	i := 0
	for i < len(e.samples) && e.samples[i].T.Before(cut) {
		i++
	}
	if i > 0 {
		e.samples = append([]Sample(nil), e.samples[i:]...)
	}
}

// History returns trend samples newer than since.
func (e *Engine) History(since time.Time) []Sample {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := []Sample{}
	for _, s := range e.samples {
		if s.T.After(since) {
			out = append(out, s)
		}
	}
	return out
}

type persisted struct {
	Version int           `json:"version"`
	Saved   time.Time     `json:"saved"`
	Store   incident.Dump `json:"store"`
	Samples []Sample      `json:"samples"`
}

// Save writes incidents and trend samples to StatePath, atomically, if
// anything changed since the last save. No-op without a StatePath.
func (e *Engine) Save() error {
	if e.cfg.StatePath == "" {
		return nil
	}
	e.mu.RLock()
	samples := append([]Sample(nil), e.samples...)
	e.mu.RUnlock()
	p := persisted{Version: 1, Store: e.store.Dump(), Samples: samples}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	e.mu.Lock()
	same := bytes.Equal(body, e.lastSaved)
	e.lastSaved = body
	e.mu.Unlock()
	if same {
		return nil
	}
	p.Saved = time.Now().UTC()
	if body, err = json.Marshal(p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.cfg.StatePath), 0o700); err != nil {
		return err
	}
	tmp := e.cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.cfg.StatePath)
}

// Restore loads a previous Save. A missing file is not an error. It
// returns how many incidents were restored. Call before Run.
func (e *Engine) Restore() (int, error) {
	if e.cfg.StatePath == "" {
		return 0, nil
	}
	body, err := os.ReadFile(e.cfg.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("state file: %w", err)
	}
	var p persisted
	if err := json.Unmarshal(body, &p); err != nil {
		return 0, fmt.Errorf("state file %s is not readable (move it aside to start fresh): %w", e.cfg.StatePath, err)
	}
	kept := p.Store.Items[:0]
	for _, sv := range p.Store.Items {
		if sv.Incident == nil || e.clusters[sv.Incident.Cluster] == nil {
			// The cluster is no longer watched; its open incidents
			// could never be resolved or verified.
			if sv.Incident != nil && sv.Incident.ClosedAt != nil {
				kept = append(kept, sv)
			}
			continue
		}
		// An explanation that was in flight when the process stopped
		// will never arrive; ask again.
		if sv.Incident.ExplanationState == incident.ExplainPending {
			sv.ExplainStarted = false
		}
		kept = append(kept, sv)
	}
	p.Store.Items = kept
	e.store.Load(p.Store)

	cut := time.Now().Add(-historyWindow)
	e.mu.Lock()
	e.samples = e.samples[:0]
	for _, s := range p.Samples {
		if s.T.After(cut) {
			e.samples = append(e.samples, s)
		}
	}
	e.mu.Unlock()
	return len(kept), nil
}
