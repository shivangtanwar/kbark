// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxAuditInMemory bounds the entries kept for the dashboard. The file
// on disk keeps everything.
const maxAuditInMemory = 500

// Entry is one line of the audit log.
type Entry struct {
	Time      time.Time `json:"time"`
	Incident  string    `json:"incident"`
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace"`
	Workload  string    `json:"workload"`
	Family    string    `json:"family"`
	Event     string    `json:"event"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action,omitempty"`
	Target    string    `json:"target,omitempty"`
	Diff      string    `json:"diff,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// Audit is an append-only JSON-lines log of every incident event:
// what was detected, what was proposed, who approved it, what changed.
type Audit struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	entries []Entry
	lastErr string
}

// OpenAudit opens (creating if needed) the audit file at path. An empty
// path keeps the log in memory only.
func OpenAudit(path string) (*Audit, error) {
	a := &Audit{path: path}
	if path == "" {
		return a, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	a.file = f
	a.entries = tail(path, maxAuditInMemory)
	return a, nil
}

// tail reads the last n entries of an existing audit file so the
// dashboard shows history from before a restart. Unparseable lines are
// skipped: the file is evidence, not a database kbark depends on.
func tail(path string, n int) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Incident != "" {
			out = append(out, e)
			if len(out) > 2*n {
				out = append([]Entry(nil), out[len(out)-n:]...)
			}
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Path returns the file path, or "" for an in-memory log.
func (a *Audit) Path() string { return a.path }

// Append writes one entry.
func (a *Audit) Append(e Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	if len(a.entries) > maxAuditInMemory {
		a.entries = a.entries[len(a.entries)-maxAuditInMemory:]
	}
	if a.file == nil {
		return
	}
	line, err := json.Marshal(e)
	if err == nil {
		_, err = a.file.Write(append(line, '\n'))
	}
	if err != nil {
		a.lastErr = err.Error()
	}
}

// Entries returns the most recent entries, newest first.
func (a *Audit) Entries() []Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Entry, len(a.entries))
	for i, e := range a.entries {
		out[len(a.entries)-1-i] = e
	}
	return out
}

// Err returns the last write error, or "".
func (a *Audit) Err() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastErr
}

// Close closes the file.
func (a *Audit) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil {
		return nil
	}
	return a.file.Close()
}
