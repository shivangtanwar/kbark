// SPDX-License-Identifier: Apache-2.0

package views_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

func TestDescribeView_initialStateShowsLoading(t *testing.T) {
	v := views.NewDescribeView(theme.Default())
	v = v.SetSize(80, 24).SetTitle("default/cause-crash · Pod")

	if !strings.Contains(v.View(), "loading describe") {
		t.Errorf("initial view should show loading state, got:\n%s", v.View())
	}
}

func TestDescribeView_setYAMLImmediatelyAvailableViaToggle(t *testing.T) {
	v := views.NewDescribeView(theme.Default())
	v = v.SetSize(80, 24).SetYAML("apiVersion: v1\nkind: Pod\nmetadata:\n  name: alpha\n")

	// Default mode is describe — YAML not visible yet.
	if strings.Contains(v.View(), "kind: Pod") {
		t.Error("ModeDescribe should hide YAML content")
	}

	v = v.ToggleMode()
	if v.Mode() != views.ModeYAML {
		t.Errorf("Mode() = %v, want ModeYAML after toggle", v.Mode())
	}
	if !strings.Contains(v.View(), "kind: Pod") {
		t.Errorf("ModeYAML should show YAML content, got:\n%s", v.View())
	}
}

func TestDescribeView_setDescribeReplacesLoading(t *testing.T) {
	v := views.NewDescribeView(theme.Default())
	v = v.SetSize(80, 24).SetDescribe("Name:   alpha\nStatus: Running\n")

	if strings.Contains(v.View(), "loading describe") {
		t.Error("after SetDescribe, loading state should be gone")
	}
	if !strings.Contains(v.View(), "Status: Running") {
		t.Errorf("describe text should be visible, got:\n%s", v.View())
	}
}

// TestDescribeView_errorFallbackHintsYAMLToggle pins the UX: when
// describe fails (apiserver hiccup, RBAC, etc.) the modal must point
// the user to `y` rather than just stranding them with an error.
func TestDescribeView_errorFallbackHintsYAMLToggle(t *testing.T) {
	v := views.NewDescribeView(theme.Default())
	v = v.SetSize(80, 24).
		SetYAML("apiVersion: v1\nkind: Pod\n").
		MarkError(errors.New("describer for Pod: forbidden"))

	view := v.View()
	if !strings.Contains(view, "describe failed") {
		t.Errorf("should surface describe error, got:\n%s", view)
	}
	if !strings.Contains(view, "press 'y'") {
		t.Errorf("should hint user to toggle to YAML, got:\n%s", view)
	}
	// YAML is still reachable via toggle.
	v = v.ToggleMode()
	if !strings.Contains(v.View(), "kind: Pod") {
		t.Errorf("YAML still reachable after describe error: view:\n%s", v.View())
	}
}

func TestDescribeView_resetClearsState(t *testing.T) {
	v := views.NewDescribeView(theme.Default())
	v = v.SetSize(80, 24).SetDescribe("first").SetYAML("yaml-first").MarkError(errors.New("oops"))
	v = v.Reset()
	v = v.SetSize(80, 24) // reset clears buffers; size re-applies
	if strings.Contains(v.View(), "first") || strings.Contains(v.View(), "yaml-first") || strings.Contains(v.View(), "oops") {
		t.Errorf("Reset() should clear prior state, got:\n%s", v.View())
	}
	if !strings.Contains(v.View(), "loading describe") {
		t.Error("Reset() should restore loading state")
	}
}
