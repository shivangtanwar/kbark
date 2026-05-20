// SPDX-License-Identifier: Apache-2.0

package views_test

import (
	"fmt"
	"testing"

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
}
