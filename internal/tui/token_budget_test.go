// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
)

func TestCheckTokenBudget_zeroDisables(t *testing.T) {
	huge := strings.Repeat("a", 1_000_000)
	if err := checkTokenBudget(0, huge, ""); err != nil {
		t.Errorf("budget=0 should disable the check, got %v", err)
	}
}

func TestCheckTokenBudget_underBudgetPasses(t *testing.T) {
	payload := strings.Repeat("a", 4_000) // ~1000 tokens
	if err := checkTokenBudget(2000, payload, ""); err != nil {
		t.Errorf("under budget, want nil, got %v", err)
	}
}

// TestCheckTokenBudget_overBudgetReturnsErrorNamingBoth pins the UX:
// the modal error must tell the user both the estimate and the
// configured budget, so they know whether to refine their selection
// or raise profile.token_budget.
func TestCheckTokenBudget_overBudgetReturnsErrorNamingBoth(t *testing.T) {
	payload := strings.Repeat("a", 12_000) // ~3000 tokens
	err := checkTokenBudget(500, payload, "system")
	if err == nil {
		t.Fatal("over budget should return an error")
	}
	msg := err.Error()
	// Estimate is payload (~3000) + system (~2) = ~3002. Just check
	// the message contains a number in that ballpark plus the budget.
	if !strings.Contains(msg, "300") {
		t.Errorf("error should report the estimate (~3000 tokens), got %q", msg)
	}
	if !strings.Contains(msg, "500") {
		t.Errorf("error should report the budget, got %q", msg)
	}
}

// TestCheckTokenBudget_countsSystemPrompt pins that the system prompt
// is included in the budget — important because it's typically the
// largest fixed piece of every request.
func TestCheckTokenBudget_countsSystemPrompt(t *testing.T) {
	system := strings.Repeat("s", 4_000) // ~1000 tokens
	// payload is small; only the system prompt should push us over.
	err := checkTokenBudget(500, "tiny", system)
	if err == nil {
		t.Errorf("expected over-budget error when system prompt alone exceeds budget")
	}
}
