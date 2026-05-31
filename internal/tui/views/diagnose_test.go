// SPDX-License-Identifier: Apache-2.0

package views_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

// TestDiagnoseView_appendTextValueSemanticsSafe is the regression test
// for the strings.Builder panic. The bubbletea Update loop returns the
// Model (and thus the DiagnoseView) by value, so AppendText is invoked
// on a fresh struct copy each time. A previous implementation that
// stored a strings.Builder by value would panic the moment Go's escape
// analysis placed the copy at a different memory address. Repeated
// AppendText calls on a value-typed view must succeed.
func TestDiagnoseView_appendTextValueSemanticsSafe(t *testing.T) {
	v := views.NewDiagnoseView(theme.Default())
	v = v.SetSize(80, 24)

	for i := 0; i < 50; i++ {
		v = v.AppendText("chunk ")
	}

	if !strings.Contains(v.View(), "chunk ") {
		t.Errorf("expected accumulated 'chunk ' fragments in view, got:\n%s", v.View())
	}
}

func TestDiagnoseView_resetClearsAccumulatedText(t *testing.T) {
	v := views.NewDiagnoseView(theme.Default())
	v = v.SetSize(80, 24)
	v = v.AppendText("hello ")
	v = v.AppendText("world")
	v = v.Reset()
	v = v.AppendText("fresh")

	rendered := v.View()
	if strings.Contains(rendered, "hello world") {
		t.Errorf("Reset() should clear prior text, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "fresh") {
		t.Errorf("post-Reset AppendText should still be visible, got:\n%s", rendered)
	}
}

func TestDiagnoseView_markDoneShowsDismissPrompt(t *testing.T) {
	v := views.NewDiagnoseView(theme.Default())
	v = v.SetSize(80, 24)
	v = v.AppendText("done diagnosing")
	v = v.MarkDone()

	if v.State() != views.DiagnoseDone {
		t.Errorf("State() = %v, want DiagnoseDone", v.State())
	}
	if !strings.Contains(v.View(), "diagnosis complete") {
		t.Errorf("Done state should show 'diagnosis complete' status, got:\n%s", v.View())
	}
}

func TestDiagnoseView_markErrorShowsErrorMessage(t *testing.T) {
	v := views.NewDiagnoseView(theme.Default())
	v = v.SetSize(80, 24)
	v = v.MarkError(errors.New("auth failed: 401"))

	if v.State() != views.DiagnoseErrored {
		t.Errorf("State() = %v, want DiagnoseErrored", v.State())
	}
	if !strings.Contains(v.View(), "auth failed: 401") {
		t.Errorf("Error state should surface the message, got:\n%s", v.View())
	}
}

// TestDiagnoseView_appendTextWrapsToWidth pins the regression where the
// AI prose ran off the right edge of the terminal — the viewport doesn't
// soft-wrap by default, so long sentences must be wrapped before
// SetContent. Visible width is measured via lipgloss.Width so styling
// ANSI sequences don't get counted as columns.
func TestDiagnoseView_appendTextWrapsToWidth(t *testing.T) {
	const width = 40
	v := views.NewDiagnoseView(theme.Default())
	v = v.SetSize(width, 24)
	v = v.AppendText(strings.Repeat("the quick brown fox jumps over the lazy dog ", 6))

	for _, line := range strings.Split(v.View(), "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line exceeds width %d: visible=%d line=%q", width, w, line)
		}
	}
}

// TestDiagnoseView_setSizeRewrapsExistingText covers the resize path: a
// viewport that started narrow and grew wider (or vice versa) must
// re-wrap its existing buffer instead of leaving stale wrap points from
// the previous width baked in.
func TestDiagnoseView_setSizeRewrapsExistingText(t *testing.T) {
	v := views.NewDiagnoseView(theme.Default())
	v = v.SetSize(20, 24)
	v = v.AppendText(strings.Repeat("alpha beta gamma delta ", 4))
	const newWidth = 60
	v = v.SetSize(newWidth, 24)

	for _, line := range strings.Split(v.View(), "\n") {
		if w := lipgloss.Width(line); w > newWidth {
			t.Errorf("after resize, line exceeds width %d: visible=%d line=%q", newWidth, w, line)
		}
	}
}
