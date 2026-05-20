// SPDX-License-Identifier: Apache-2.0

package views_test

import (
	"errors"
	"strings"
	"testing"

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
