// SPDX-License-Identifier: Apache-2.0

package views_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

func TestLogsView_appendAccumulates(t *testing.T) {
	v := views.NewLogsView(theme.Default())
	v = v.SetSize(80, 10)
	v = v.AppendLines([]string{"a", "b"})
	v = v.AppendLines([]string{"c"})
	if v.LineCount() != 3 {
		t.Errorf("LineCount = %d, want 3", v.LineCount())
	}
}

func TestLogsView_evictsOldestPastCap(t *testing.T) {
	v := views.NewLogsView(theme.Default())
	v = v.SetSize(80, 10)

	batch := make([]string, views.LogsViewMaxLines+200)
	for i := range batch {
		batch[i] = fmt.Sprintf("line-%05d", i)
	}
	v = v.AppendLines(batch)

	if v.LineCount() != views.LogsViewMaxLines {
		t.Errorf("LineCount = %d, want %d", v.LineCount(), views.LogsViewMaxLines)
	}
}

func TestLogsView_followToggleFlipsState(t *testing.T) {
	v := views.NewLogsView(theme.Default())
	if !v.Following() {
		t.Fatal("new view should default to follow=true")
	}
	v = v.ToggleFollow()
	if v.Following() {
		t.Fatal("after one toggle, follow should be false")
	}
	v = v.ToggleFollow()
	if !v.Following() {
		t.Fatal("after two toggles, follow should be true again")
	}
}

func TestLogsView_resetClearsState(t *testing.T) {
	v := views.NewLogsView(theme.Default())
	v = v.SetSize(80, 10)
	v = v.AppendLines([]string{"a", "b", "c"})
	if v.LineCount() != 3 {
		t.Fatal("setup failed")
	}
	v = v.Reset()
	if v.LineCount() != 0 {
		t.Errorf("LineCount after Reset = %d, want 0", v.LineCount())
	}
	if v.Cursor() != -1 {
		t.Errorf("Cursor after Reset = %d, want -1", v.Cursor())
	}
}

// TestLogsView_cursorTracksBottomInFollowMode pins the streaming UX:
// while follow is on, each new line snaps the cursor to the newest
// entry. The user can `?` on the latest entry without manually
// navigating.
func TestLogsView_cursorTracksBottomInFollowMode(t *testing.T) {
	v := views.NewLogsView(theme.Default())
	v = v.SetSize(80, 10).
		AppendLines([]string{"a", "b", "c"})

	if v.Cursor() != 2 {
		t.Errorf("after append, cursor = %d, want 2 (last line)", v.Cursor())
	}

	v = v.AppendLines([]string{"d"})
	if v.Cursor() != 3 {
		t.Errorf("after second append in follow mode, cursor = %d, want 3", v.Cursor())
	}
}

// TestLogsView_kMovesCursorUpAndDisablesFollow pins the manual
// navigation path that the `?` flow depends on.
func TestLogsView_kMovesCursorUpAndDisablesFollow(t *testing.T) {
	v := views.NewLogsView(theme.Default())
	v = v.SetSize(80, 10).
		AppendLines([]string{"a", "b", "c", "d", "e"})

	if !v.Following() {
		t.Fatal("expected follow=true after initial append")
	}

	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if v.Cursor() != 3 {
		t.Errorf("after k, cursor = %d, want 3", v.Cursor())
	}
	if v.Following() {
		t.Error("after k off the bottom, follow should turn off")
	}

	// New lines shouldn't snap the cursor while follow is off.
	v = v.AppendLines([]string{"f", "g"})
	if v.Cursor() != 3 {
		t.Errorf("after appends with follow=false, cursor = %d, want 3 (unchanged)", v.Cursor())
	}
}

// TestLogsView_selectedLineAndLinesAroundForDiagnose pins the API
// the `?` handler in app.go calls. The window must include `before`
// lines before, the focus line, and `after` lines after — clamped
// at buffer edges so a cursor near the top or bottom still returns
// a usable slice.
func TestLogsView_selectedLineAndLinesAroundForDiagnose(t *testing.T) {
	lines := []string{"l0", "l1", "l2", "l3", "l4", "l5", "l6"}
	v := views.NewLogsView(theme.Default()).SetSize(80, 10).AppendLines(lines)

	// Move cursor to l3 (index 3).
	for i := 0; i < 3; i++ {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	}
	line, idx, ok := v.SelectedLine()
	if !ok {
		t.Fatal("SelectedLine ok=false; expected true")
	}
	if line != "l3" || idx != 3 {
		t.Errorf("SelectedLine = (%q,%d), want (\"l3\",3)", line, idx)
	}

	window := v.LinesAround(idx, 2, 2)
	if got := strings.Join(window, ","); got != "l1,l2,l3,l4,l5" {
		t.Errorf("LinesAround(3, 2, 2) = %q, want l1,l2,l3,l4,l5", got)
	}

	// Edge: top clamp.
	top := v.LinesAround(0, 2, 2)
	if got := strings.Join(top, ","); got != "l0,l1,l2" {
		t.Errorf("LinesAround(0, 2, 2) = %q, want l0,l1,l2", got)
	}

	// Edge: bottom clamp.
	bot := v.LinesAround(6, 2, 2)
	if got := strings.Join(bot, ","); got != "l4,l5,l6" {
		t.Errorf("LinesAround(6, 2, 2) = %q, want l4,l5,l6", got)
	}
}
