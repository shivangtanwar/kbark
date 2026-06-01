// SPDX-License-Identifier: Apache-2.0

package theme_test

import (
	"testing"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

func TestResolveByName_defaultEmpty(t *testing.T) {
	if got := theme.ResolveByName(""); got.PhaseRunning.GetForeground() == nil {
		t.Error("Default theme should expose a non-nil foreground for PhaseRunning")
	}
}

func TestResolveByName_highContrast(t *testing.T) {
	hc := theme.ResolveByName("high-contrast")
	def := theme.Default()
	// Sanity: high-contrast Failed colour differs from default's.
	// (Default uses 196; HC uses 9 — different ANSI codes.)
	if hc.StatusFail.GetForeground() == def.StatusFail.GetForeground() {
		t.Errorf("high-contrast theme should differ from default; both render StatusFail as %v",
			hc.StatusFail.GetForeground())
	}
	if got := theme.ResolveByName("hc"); got.StatusFail.GetForeground() != hc.StatusFail.GetForeground() {
		t.Error("hc alias should resolve to the same theme as high-contrast")
	}
}

// TestResolveByName_unknownFallsBackToDefault pins the defensive
// behaviour: a typo in profile.theme shouldn't crash startup.
func TestResolveByName_unknownFallsBackToDefault(t *testing.T) {
	got := theme.ResolveByName("space-cadet")
	def := theme.Default()
	if got.StatusFail.GetForeground() != def.StatusFail.GetForeground() {
		t.Error("unknown theme name should fall back to Default")
	}
}
