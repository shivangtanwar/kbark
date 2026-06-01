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

// keywords is the case-insensitive alternation matched by both
// patterns. Centralised so the inline and multiline regexes stay in
// sync.
const keywords = `password|passwd|pwd|token|secret|api[-_]?key|api[-_]?token|access[-_]?key|access[-_]?token|bearer|authorization`

// inlineMatcher catches the common `key: value` / `key=value` shapes
// where key and value are on the same line. Separator whitespace is
// restricted to `[ \t]` (no newlines) so we never cross logical
// boundaries — the multilineMatcher handles the kubectl-describe
// shape where value lives on the next line.
//
// Groups:
//
//	1: keyword (e.g., "password", "api_key", "Authorization")
//	2: separator (": ", "=", trailing key-quote then colon)
//	3: optional opening value quote
//	4: value content (replaced with Placeholder)
//
// Trailing `\b` anchors the keyword so "secret-store: vault" doesn't
// match. No leading boundary because env-var keys sit right after
// an underscore (`DB_PASSWORD`, `JWT_SECRET`) and `\b` doesn't fire
// at an underscore→letter transition.
var inlineMatcher = regexp.MustCompile(`(?i)(` + keywords + `)\b(["']?[ \t]*[:=][ \t]*)(["']?)([^"'\s,;}\]\n\r][^"',;}\]\n\r]*)`)

// multilineMatcher catches the kubectl-describe ConfigMap/Secret
// shape:
//
//	api_token:
//	----
//	ya29.abc
//
// The key sits on its own line ending with `:`, optionally followed
// by a `----` separator line, then the value on the line after. The
// inline matcher must NOT touch this shape — its `[ \t]*` separator
// guarantees that, since the newline after `:` can't be consumed.
//
// `[^:\n]*?` (lazy, non-greedy) absorbs everything before the
// keyword on the same line, so embedded forms like `database_password:`
// and `JWT_API_TOKEN:` match the same way they would inline. The
// prefix is preserved verbatim in the replacement.
//
// Groups:
//
//	1: line prefix (chars before the keyword on the key line)
//	2: keyword
//	3: value-line indent
//	4: value content (replaced with Placeholder)
var multilineMatcher = regexp.MustCompile(`(?im)^([^:\n]*?)(` + keywords + `)\b[ \t]*:[ \t]*$\n(?:[ \t]*-{2,}[ \t]*\n)?([ \t]*)([^\s][^\n]*)`)

// Redact returns text with sensitive-looking values replaced by
// Placeholder. Runs the multilineMatcher first (kubectl-describe
// shape) so the inline pass that follows doesn't grab the `----`
// separator as a value. Pure function, safe to call on arbitrary
// input.
func Redact(text string) string {
	if text == "" {
		return text
	}

	// Pass 1: kubectl-describe shape (key on its own line, optional
	// `----` separator, value on next line). Replace the value line.
	text = multilineMatcher.ReplaceAllStringFunc(text, func(match string) string {
		sub := multilineMatcher.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		prefix, key, valIndent, value := sub[1], sub[2], sub[3], sub[4]
		if isAlreadyRedacted(value) {
			return match
		}
		// Reconstruct: keep the line prefix + key + trailing colon,
		// plus the value-line indent, then the placeholder. Drop
		// the `----` separator if present — it carried no
		// information once the value is gone.
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString(key)
		b.WriteString(":\n")
		b.WriteString(valIndent)
		b.WriteString(Placeholder)
		return b.String()
	})

	// Pass 2: same-line `key: value` / `key=value` shapes.
	return inlineMatcher.ReplaceAllStringFunc(text, func(match string) string {
		sub := inlineMatcher.FindStringSubmatch(match)
		if len(sub) < 5 {
			return match
		}
		key, sep, quote, value := sub[1], sub[2], sub[3], sub[4]
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
