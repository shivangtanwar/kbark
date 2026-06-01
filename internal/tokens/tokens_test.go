// SPDX-License-Identifier: Apache-2.0

package tokens_test

import (
	"strings"
	"testing"

	"github.com/shivangtanwar/kbark/internal/tokens"
)

func TestEstimate_emptyZero(t *testing.T) {
	if got := tokens.Estimate(""); got != 0 {
		t.Errorf("Estimate(\"\") = %d, want 0", got)
	}
}

func TestEstimate_roundsUp(t *testing.T) {
	// 3 chars is < 1 token by the strict heuristic, but we round up so
	// micro-payloads still cost something — important for keeping the
	// budget honest on small but numerous tool results.
	if got := tokens.Estimate("abc"); got != 1 {
		t.Errorf("Estimate(\"abc\") = %d, want 1 (rounded up)", got)
	}
}

func TestEstimate_typicalPayload(t *testing.T) {
	s := strings.Repeat("a", 4000) // 4000 chars
	if got := tokens.Estimate(s); got != 1000 {
		t.Errorf("Estimate(4000 chars) = %d, want 1000", got)
	}
}

func TestEstimateAll_sumsParts(t *testing.T) {
	got := tokens.EstimateAll("abcd", "efgh") // 4 + 4 = 8 chars = 2 tokens
	if got != 2 {
		t.Errorf("EstimateAll = %d, want 2", got)
	}
}
