// SPDX-License-Identifier: Apache-2.0

package transcript_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shivangtanwar/kbark/internal/transcript"
)

func newRecorder() *transcript.Recorder {
	r := transcript.New(transcript.Header{
		Origin:    transcript.OriginPod,
		Kind:      "Pod",
		Namespace: "default",
		Name:      "cause-crash",
		Provider:  "anthropic",
		Model:     "claude-sonnet-4-6",
	})
	r.SetSystemPrompt("You are an expert Kubernetes operator.")
	r.SetPayload("## Pod: default/cause-crash\n...")
	r.AppendDelta("The pod is in CrashLoopBackOff because…")
	r.AppendToolCall("get_previous_logs")
	r.MarkDone()
	return r
}

func TestRecorder_renderIncludesAllSections(t *testing.T) {
	out := newRecorder().Render()

	for _, want := range []string{
		"# kbark diagnosis · default/cause-crash · Pod",
		"- **Provider**: anthropic claude-sonnet-4-6",
		"- **Origin**: pod",
		"## System prompt",
		"You are an expert Kubernetes operator.",
		"## Context payload",
		"## Pod: default/cause-crash",
		"## Diagnosis",
		"The pod is in CrashLoopBackOff",
		"## Tool calls",
		"`get_previous_logs`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q.\nGot:\n%s", want, out)
		}
	}
}

// TestRecorder_logOriginIncludesLineNumber pins the log-flow variant
// of the transcript title and filename.
func TestRecorder_logOriginIncludesLineNumber(t *testing.T) {
	r := transcript.New(transcript.Header{
		Origin:     transcript.OriginLog,
		Kind:       "Pod",
		Namespace:  "default",
		Name:       "cause-crash",
		LogLineIdx: 42,
	})
	r.SetPayload("log focus content")
	r.MarkDone()

	out := r.Render()
	if !strings.Contains(out, "log line 42") {
		t.Errorf("log transcript title should include line number, got:\n%s", out)
	}
}

// TestRecorder_markErrorStillSaves pins the contract that a failed
// session is still written to disk — the user wants to see the
// payload and error to understand what happened.
func TestRecorder_markErrorStillSaves(t *testing.T) {
	r := transcript.New(transcript.Header{Origin: transcript.OriginPod, Kind: "Pod", Name: "x"})
	r.SetPayload("payload")
	r.MarkError(errors.New("rate limit"))

	dir := t.TempDir()
	path, err := r.Save(dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if path == "" {
		t.Fatal("empty path returned; expected save")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), "rate limit") {
		t.Errorf("error not surfaced in transcript:\n%s", body)
	}
}

// TestRecorder_disabledEnvSkipsWrite pins the opt-out path: with
// KBARK_TRANSCRIPTS=off the recorder still buffers (so the UI shows
// the diagnosis live) but Save is a no-op.
func TestRecorder_disabledEnvSkipsWrite(t *testing.T) {
	t.Setenv("KBARK_TRANSCRIPTS", "off")
	r := newRecorder()
	dir := t.TempDir()
	path, err := r.Save(dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if path != "" {
		t.Errorf("Save returned %q; expected empty (disabled)", path)
	}
	// Dir should be untouched.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("dir non-empty after disabled Save: %v", entries)
	}
}

// TestRecorder_emptyPayloadSkipsWrite guards against a recorder
// flushed before any real session content was set (e.g. AI was
// unconfigured and the modal opened with an immediate error).
func TestRecorder_emptyPayloadSkipsWrite(t *testing.T) {
	r := transcript.New(transcript.Header{Origin: transcript.OriginPod, Kind: "Pod", Name: "x"})
	r.MarkError(errors.New("AI not configured"))

	dir := t.TempDir()
	path, err := r.Save(dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path on empty payload, got %q", path)
	}
}

// TestRecorder_filenameSanitisesUnsafeChars pins the contract that
// resource names with /, spaces, or other shell-unfriendly chars
// don't produce broken filenames.
func TestRecorder_filenameSanitisesUnsafeChars(t *testing.T) {
	r := transcript.New(transcript.Header{
		Origin: transcript.OriginResource,
		Kind:   "ConfigMap",
		Name:   "weird/name with spaces.&stuff",
	})
	r.SetPayload("x")
	r.MarkDone()

	dir := t.TempDir()
	path, err := r.Save(dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	name := filepath.Base(path)
	for _, bad := range []string{"/", " ", "&", ":"} {
		if strings.Contains(name, bad) {
			t.Errorf("filename %q contains unsafe character %q", name, bad)
		}
	}
	if !strings.HasSuffix(name, ".md") {
		t.Errorf("filename %q missing .md extension", name)
	}
}
