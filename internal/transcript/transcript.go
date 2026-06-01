// SPDX-License-Identifier: Apache-2.0

// Package transcript records each `?` diagnosis as a markdown file
// under the user's cache dir. The user can review what kbark sent to
// the model, the model's tool calls, and the final answer — useful
// for incident docs, sharing diagnoses, or auditing the AI output.
//
// Per decision 12 in the project plan: writes are opt-out, gated by
// the KBARK_TRANSCRIPTS env var (set to "off" / "false" / "0" to
// disable). M8's profile system will replace the env var with a
// proper config knob.
package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// EnvDisable is the env var name that, when set to a falsy value,
// suppresses transcript writes entirely. Empty / unset means
// transcripts are on.
const EnvDisable = "KBARK_TRANSCRIPTS"

// Origin describes which `?` flow produced the transcript.
type Origin string

const (
	OriginPod      Origin = "pod"
	OriginResource Origin = "resource"
	OriginLog      Origin = "log"
)

// Recorder buffers everything that happens during a diagnose session
// and emits a markdown transcript on Finalize. Use one per session;
// the app creates a fresh Recorder on each `?` press.
//
// The Recorder is goroutine-safe for the access pattern actually used
// by the TUI: only the bubbletea Update goroutine writes to it (each
// Append* / Mark* call happens during a single Update handler), and
// Finalize is called from that same goroutine on DoneEvent.
type Recorder struct {
	header    Header
	system    string
	payload   string
	delta     strings.Builder
	toolCalls []ToolCall
	startedAt time.Time
	finished  bool
	err       error
}

// Header is the metadata captured at session start.
type Header struct {
	Origin     Origin
	Kind       string // e.g. "Pod", "Deployment", "Node"
	Namespace  string // empty for cluster-scoped or log-line variants
	Name       string
	Provider   string // e.g. "anthropic"
	Model      string // e.g. "claude-sonnet-4-6"
	LogLineIdx int    // OriginLog only; -1 otherwise
}

// ToolCall is one breadcrumb the model emitted.
type ToolCall struct {
	Name    string
	When    time.Time
	Elapsed time.Duration // since session start
}

// New starts a recorder for a new session.
func New(h Header) *Recorder {
	if h.LogLineIdx == 0 && h.Origin != OriginLog {
		h.LogLineIdx = -1
	}
	return &Recorder{
		header:    h,
		startedAt: time.Now(),
	}
}

// SetSystemPrompt records the system prompt sent to the model. Called
// once at session open.
func (r *Recorder) SetSystemPrompt(s string) {
	if r == nil {
		return
	}
	r.system = s
}

// SetPayload records the user-message payload sent to the model.
// Called once at session open.
func (r *Recorder) SetPayload(s string) {
	if r == nil {
		return
	}
	r.payload = s
}

// AppendDelta appends a streamed-text chunk to the running answer.
func (r *Recorder) AppendDelta(delta string) {
	if r == nil {
		return
	}
	r.delta.WriteString(delta)
}

// AppendToolCall records a tool-call breadcrumb.
func (r *Recorder) AppendToolCall(name string) {
	if r == nil {
		return
	}
	r.toolCalls = append(r.toolCalls, ToolCall{
		Name:    name,
		When:    time.Now(),
		Elapsed: time.Since(r.startedAt),
	})
}

// MarkError captures a session-level error (e.g. apiserver hiccup,
// rate limit). The transcript still saves so the user can see what
// happened.
func (r *Recorder) MarkError(err error) {
	if r == nil {
		return
	}
	r.err = err
	r.finished = true
}

// MarkDone signals successful completion.
func (r *Recorder) MarkDone() {
	if r == nil {
		return
	}
	r.finished = true
}

// Render produces the markdown text without writing it.
func (r *Recorder) Render() string {
	var out strings.Builder
	h := r.header
	titleSubject := h.Name
	if h.Namespace != "" {
		titleSubject = h.Namespace + "/" + h.Name
	}
	if h.Origin == OriginLog && h.LogLineIdx >= 0 {
		titleSubject = fmt.Sprintf("%s · log line %d", titleSubject, h.LogLineIdx)
	}
	fmt.Fprintf(&out, "# kbark diagnosis · %s · %s\n\n", titleSubject, h.Kind)

	fmt.Fprintf(&out, "- **Captured**: %s\n", r.startedAt.UTC().Format(time.RFC3339))
	if h.Provider != "" {
		fmt.Fprintf(&out, "- **Provider**: %s %s\n", h.Provider, h.Model)
	}
	fmt.Fprintf(&out, "- **Origin**: %s\n", h.Origin)
	if r.err != nil {
		fmt.Fprintf(&out, "- **Error**: %s\n", r.err.Error())
	}
	fmt.Fprintln(&out)

	if r.system != "" {
		fmt.Fprintln(&out, "## System prompt")
		fmt.Fprintln(&out)
		writeFenced(&out, r.system)
	}

	if r.payload != "" {
		fmt.Fprintln(&out, "## Context payload")
		fmt.Fprintln(&out)
		writeFenced(&out, r.payload)
	}

	fmt.Fprintln(&out, "## Diagnosis")
	fmt.Fprintln(&out)
	if r.delta.Len() == 0 {
		fmt.Fprintln(&out, "_(no text returned)_")
	} else {
		fmt.Fprintln(&out, strings.TrimRight(r.delta.String(), "\n"))
	}
	fmt.Fprintln(&out)

	if len(r.toolCalls) > 0 {
		fmt.Fprintln(&out, "## Tool calls")
		fmt.Fprintln(&out)
		for _, tc := range r.toolCalls {
			fmt.Fprintf(&out, "- `%s` at +%s\n", tc.Name, tc.Elapsed.Truncate(100*time.Millisecond))
		}
	}

	return out.String()
}

func writeFenced(out *strings.Builder, body string) {
	fmt.Fprintln(out, "```")
	fmt.Fprintln(out, strings.TrimRight(body, "\n"))
	fmt.Fprintln(out, "```")
	fmt.Fprintln(out)
}

// Disabled reports whether the EnvDisable env var asks us to skip
// transcript writes.
func Disabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvDisable)))
	switch v {
	case "", "1", "true", "on", "yes":
		return false
	default:
		return true
	}
}

// DefaultDir returns the platform-appropriate directory for kbark
// transcripts. Linux: ~/.cache/kbark/diagnoses. macOS: ~/Library/
// Caches/kbark/diagnoses. Windows: %LocalAppData%/kbark/diagnoses.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("user cache dir: %w", err)
	}
	return filepath.Join(base, "kbark", "diagnoses"), nil
}

// Save writes the recorder's markdown to dir/<auto-filename> and
// returns the full path. The directory is created if missing. No-op
// (returns "", nil) when transcripts are disabled via env or the
// recorder is empty (no payload).
func (r *Recorder) Save(dir string) (string, error) {
	if r == nil || Disabled() || r.payload == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir transcript dir: %w", err)
	}
	path := filepath.Join(dir, r.filename())
	if err := os.WriteFile(path, []byte(r.Render()), 0o644); err != nil {
		return "", fmt.Errorf("write transcript: %w", err)
	}
	return path, nil
}

// filename is the auto-generated filename for the transcript:
// "<ts>_<kind>_<safe-name>[.log_line_<N>].md". Sortable, identifies
// the subject at a glance, safe for case-insensitive filesystems.
func (r *Recorder) filename() string {
	ts := r.startedAt.UTC().Format("20060102T150405")
	kind := strings.ToLower(r.header.Kind)
	if kind == "" {
		kind = "unknown"
	}
	name := safeFilename(r.header.Name)
	out := fmt.Sprintf("%s_%s_%s", ts, kind, name)
	if r.header.Origin == OriginLog && r.header.LogLineIdx >= 0 {
		out = fmt.Sprintf("%s_line%d", out, r.header.LogLineIdx)
	}
	return out + ".md"
}

// safeFilename strips characters that misbehave on common filesystems
// (Windows in particular). Replaces with `_`. Caps at 80 chars so a
// long resource name doesn't blow past path limits.
var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeFilename(s string) string {
	if s == "" {
		return "anonymous"
	}
	out := unsafeChars.ReplaceAllString(s, "_")
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
