// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckConfig_loadedFileGreen(t *testing.T) {
	rows := checkConfig(Options{
		ConfigPath:   "/home/u/.config/kbark/config.yaml",
		ConfigLoaded: true,
		Profile:      "dev",
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
	})
	if got := find(rows, "config"); got.Status != Green || !strings.Contains(got.Detail, "config.yaml") {
		t.Errorf("config row = %+v, want green + file path", got)
	}
	if got := find(rows, "profile"); got.Status != Green || !strings.Contains(got.Detail, "dev → anthropic claude-sonnet-4-6") {
		t.Errorf("profile row = %+v", got)
	}
}

// TestCheckConfig_tokenBudgetSurfacedInProfileRow pins the M8.4
// detail extension: when the profile sets a non-zero token_budget,
// the doctor row surfaces it so the user can see the cap without
// grepping the YAML.
func TestCheckConfig_tokenBudgetSurfacedInProfileRow(t *testing.T) {
	rows := checkConfig(Options{
		ConfigPath:   "/cfg",
		ConfigLoaded: true,
		Profile:      "prod",
		Provider:     "anthropic",
		Model:        "claude-opus-4-7",
		TokenBudget:  50000,
	})
	got := find(rows, "profile")
	if !strings.Contains(got.Detail, "budget: 50000 tokens") {
		t.Errorf("profile row should surface budget, got %q", got.Detail)
	}
}

// TestCheckConfig_zeroBudgetOmitsMarker keeps the default row terse —
// no "(budget: 0 tokens)" clutter when the feature isn't in use.
func TestCheckConfig_zeroBudgetOmitsMarker(t *testing.T) {
	rows := checkConfig(Options{
		ConfigPath:   "/cfg",
		ConfigLoaded: true,
		Profile:      "dev",
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
		TokenBudget:  0,
	})
	got := find(rows, "profile")
	if strings.Contains(got.Detail, "budget") {
		t.Errorf("zero budget should not show in row, got %q", got.Detail)
	}
}

func TestCheckConfig_missingFileGreenWithNote(t *testing.T) {
	rows := checkConfig(Options{
		ConfigPath:   "/home/u/.config/kbark/config.yaml",
		ConfigLoaded: false,
		Profile:      "dev",
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
	})
	got := find(rows, "config")
	if got.Status != Green {
		t.Errorf("missing-file should still be Green (defaults work), got %v", got.Status)
	}
	if !strings.Contains(got.Detail, "built-in defaults") {
		t.Errorf("detail should mention built-in defaults, got %q", got.Detail)
	}
}

func TestCheckConfig_profileErrRedRow(t *testing.T) {
	rows := checkConfig(Options{
		ConfigPath:   "/cfg",
		ConfigLoaded: true,
		ProfileErr:   errors.New(`unknown profile "staging"`),
	})
	got := find(rows, "profile")
	if got.Status != Red {
		t.Errorf("profile row = %v, want Red", got.Status)
	}
	if !strings.Contains(got.Detail, "staging") {
		t.Errorf("error message should be surfaced, got %q", got.Detail)
	}
}

// TestMarkActive_appendsModelTagOnGreenMatch pins the active-provider
// marker — a user with valid creds for three providers should see at
// a glance which one their profile selected.
func TestMarkActive_appendsModelTagOnGreenMatch(t *testing.T) {
	in := Result{Name: "anthropic", Status: Green, Detail: "reachable"}
	got := markActive(in, Options{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	if !strings.Contains(got.Detail, "active") || !strings.Contains(got.Detail, "claude-sonnet-4-6") {
		t.Errorf("expected 'active' + model in detail, got %q", got.Detail)
	}
}

func TestMarkActive_doesNotMarkNonActiveProvider(t *testing.T) {
	in := Result{Name: "openai", Status: Green, Detail: "reachable"}
	got := markActive(in, Options{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	if strings.Contains(got.Detail, "active") {
		t.Errorf("non-active provider should not be marked, got %q", got.Detail)
	}
}

// TestMarkActive_skipsRedActiveProvider pins UX: when the active
// provider's check is RED (auth failed, unreachable), the existing
// failure detail is already enough — we don't pile "· active" on top
// of an error message.
func TestMarkActive_skipsRedActiveProvider(t *testing.T) {
	in := Result{Name: "anthropic", Status: Red, Detail: "ANTHROPIC_API_KEY unset"}
	got := markActive(in, Options{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	if strings.Contains(got.Detail, "active") {
		t.Errorf("RED active provider should not be marked, got %q", got.Detail)
	}
}

func find(rows []Result, name string) Result {
	for _, r := range rows {
		if r.Name == name {
			return r
		}
	}
	return Result{}
}
