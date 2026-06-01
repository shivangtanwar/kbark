// SPDX-License-Identifier: Apache-2.0

// Package redact scrubs sensitive-looking values out of text before
// it leaves kbark for the AI provider or the on-disk transcript.
// The strategy is intentionally simple: a small allow-list of key
// names (password, token, secret, api_key, bearer, authorization)
// matched in common k:v shapes (YAML, JSON, env-style, HTTP headers).
// Anything outside the patterns goes through untouched — kbark's
// payloads are mostly k8s describe output and log tails, which
// already strip secret VALUES via the Secrets plugin and YAML
// serialisation; this layer catches the residual cases (ConfigMap
// values that hold tokens, container args with credentials, log
// lines that print bearer tokens).
//
// False positives are preferable to false negatives: better to
// redact a legitimately-named "token: foo" than to let a real one
// through. M9 polish can refine with entropy heuristics if needed.
package redact

import (
	"regexp"
	"strings"
)

// Placeholder is what we substitute for redacted values. Chosen so
// the result is still readable and the model can reason about
// "there's a value here, it was redacted" without seeing it.
const Placeholder = "<redacted>"

// matcher is the master regex. Captures four groups:
//
//	1: the matched key text (e.g., "password", "api_key", "Authorization")
//	2: separator + optional whitespace (": ", "=", ":\t"); may include
//	   the closing quote of a JSON-style key ("password":)
//	3: optional opening quote of the value (`"`, `'`, or empty)
//	4: the value content (will be replaced)
//
// Trailing word-boundary anchors the key so "secret-store: vault"
// doesn't trigger (the `:` after "secret" would, but the separator
// requirement falls through because "-" sits between them). No
// leading boundary because env-var naming places the key right
// after an underscore (`DB_PASSWORD`, `JWT_SECRET`, `OPENAI_API_KEY`)
// and `\b` doesn't recognise the underscore→letter transition as a
// word boundary.
//
// The value class allows spaces so HTTP headers like
// `Authorization: Bearer <token>` redact the whole credential
// (keyword + token), and YAML values with internal whitespace are
// captured fully. Newlines and structural chars (commas, semicolons,
// braces, brackets) terminate the match so we never cross logical
// boundaries.
var matcher = regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|api[-_]?key|api[-_]?token|access[-_]?key|access[-_]?token|bearer|authorization)\b(["']?\s*[:=]\s*)(["']?)([^"'\s,;}\]\n\r][^"',;}\]\n\r]*)`)

// Redact returns text with sensitive-looking values replaced by
// Placeholder. Pure function, safe to call on arbitrary input.
func Redact(text string) string {
	if text == "" {
		return text
	}
	return matcher.ReplaceAllStringFunc(text, func(match string) string {
		sub := matcher.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		key, sep, quote, value := sub[1], sub[2], sub[3], sub[4]
		// Don't redact obvious non-secret placeholders that already
		// indicate redaction or absence — keeps repeated passes
		// idempotent and avoids cluttering "<none>" → "<redacted>".
		if isAlreadyRedacted(value) {
			return match
		}
		var b strings.Builder
		b.Grow(len(key) + len(sep) + len(quote)*2 + len(Placeholder))
		b.WriteString(key)
		b.WriteString(sep)
		b.WriteString(quote)
		b.WriteString(Placeholder)
		b.WriteString(quote)
		return b.String()
	})
}

func isAlreadyRedacted(v string) bool {
	switch strings.ToLower(v) {
	case "<redacted>", "<none>", "<nil>", "null", "~", "":
		return true
	}
	return false
}
