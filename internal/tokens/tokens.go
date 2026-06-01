// SPDX-License-Identifier: Apache-2.0

// Package tokens provides a cheap pre-send estimate of how many
// tokens a piece of text will consume against an LLM provider. Real
// tokenization is provider-specific (tiktoken for OpenAI, Claude's
// own tokenizer for Anthropic) and costs either a library dependency
// or an API call — too heavy for a budget pre-check that fires on
// every `?`. The chars/4 heuristic is the industry-standard rule of
// thumb and is within ~20% of true counts for typical English text;
// good enough to refuse genuinely-runaway payloads while staying out
// of the way of normal-sized diagnoses.
package tokens

// CharsPerToken is the rough English heuristic. Tighter than reality
// for code (which has more short tokens) and looser for prose. Our
// payloads are a mix; this central constant lives here so the
// budget code stays grep-able.
const CharsPerToken = 4

// Estimate returns the approximate token count of s.
func Estimate(s string) int {
	if s == "" {
		return 0
	}
	// Round up so a 3-char string still costs at least 1 token.
	n := len(s) / CharsPerToken
	if len(s)%CharsPerToken != 0 {
		n++
	}
	return n
}

// EstimateAll sums Estimate over multiple strings — convenient for
// "payload + system prompt" budget checks without intermediate
// concatenation.
func EstimateAll(parts ...string) int {
	total := 0
	for _, p := range parts {
		total += Estimate(p)
	}
	return total
}
