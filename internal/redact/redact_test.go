// SPDX-License-Identifier: Apache-2.0

package redact_test

import (
	"strings"
	"testing"

	"github.com/shivangtanwar/kbark/internal/redact"
)

// TestRedact_commonPatterns walks the canonical k:v shapes a secret
// might appear in — YAML, JSON, env-style, HTTP headers. Each case
// asserts the raw value is gone and the placeholder is in.
func TestRedact_commonPatterns(t *testing.T) {
	cases := []struct {
		name string
		in   string
		raw  string // the value that must NOT survive in the output
	}{
		{"yaml password", `password: hunter2`, "hunter2"},
		{"yaml password quoted", `password: "hunter2"`, "hunter2"},
		{"yaml password single-quoted", `password: 'hunter2'`, "hunter2"},
		{"yaml token", `token: abc.def.ghi`, "abc.def.ghi"},
		{"yaml api_key", `api_key: xyz123`, "xyz123"},
		{"yaml apikey", `apiKey: xyz123`, "xyz123"},
		{"json secret", `"secret": "topsecret"`, "topsecret"},
		{"env-style", `DB_PASSWORD=hunter2`, "hunter2"},
		{"http bearer", `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9`, "eyJhbGciOiJIUzI1NiJ9"},
		{"access_token", `access_token: ya29.a0AfH6SM`, "ya29.a0AfH6SM"},
		{"case insensitive key", `Password: hunter2`, "hunter2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := redact.Redact(tc.in)
			if strings.Contains(out, tc.raw) {
				t.Errorf("raw value %q leaked through redaction.\nin:  %q\nout: %q", tc.raw, tc.in, out)
			}
			if !strings.Contains(out, redact.Placeholder) {
				t.Errorf("placeholder %q missing from output.\nin:  %q\nout: %q", redact.Placeholder, tc.in, out)
			}
		})
	}
}

// TestRedact_preservesSurroundingContext pins the contract: only the
// VALUE is replaced; the key, separator, and surrounding fields are
// intact so the AI can still reason about "this resource has a
// password field" without seeing the secret.
func TestRedact_preservesSurroundingContext(t *testing.T) {
	in := `Containers:
  app:
    env:
      DB_HOST: db.internal
      DB_PASSWORD: hunter2
      LOG_LEVEL: info`
	out := redact.Redact(in)

	for _, want := range []string{
		"Containers:",
		"db.internal",
		"DB_PASSWORD",
		redact.Placeholder,
		"LOG_LEVEL: info",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("password value leaked:\n%s", out)
	}
}

// TestRedact_ignoresAlreadyRedactedValues pins idempotency — running
// Redact twice produces the same output, and "<none>" / null values
// don't get gratuitously replaced with "<redacted>".
func TestRedact_ignoresAlreadyRedactedValues(t *testing.T) {
	once := redact.Redact(`token: secret-value`)
	twice := redact.Redact(once)
	if once != twice {
		t.Errorf("Redact is not idempotent.\nonce:  %q\ntwice: %q", once, twice)
	}

	for _, harmless := range []string{
		`token: <none>`,
		`secret: <nil>`,
		`api_key: null`,
		`password: ""`,
	} {
		out := redact.Redact(harmless)
		if strings.Contains(out, "<redacted>") {
			t.Errorf("benign placeholder rewritten: in=%q out=%q", harmless, out)
		}
	}
}

// TestRedact_avoidsHyphenatedFalsePositives pins the word-boundary
// behaviour — names like "secret-store" / "token-bucket" should NOT
// trigger redaction.
func TestRedact_avoidsHyphenatedFalsePositives(t *testing.T) {
	cases := []string{
		`secret-store: vault.local`,
		`token-bucket: rate-limiter`,
		`my-secrets-volume: configmap`,
	}
	for _, in := range cases {
		out := redact.Redact(in)
		if strings.Contains(out, "<redacted>") {
			t.Errorf("false positive on hyphenated name.\nin:  %q\nout: %q", in, out)
		}
	}
}

func TestRedact_emptyInput(t *testing.T) {
	if got := redact.Redact(""); got != "" {
		t.Errorf("Redact(\"\") = %q, want empty", got)
	}
}

// TestRedact_passesNonSecretText pins that ordinary k8s describe
// output (events, container state, resource limits) flows through
// without any spurious redaction.
func TestRedact_passesNonSecretText(t *testing.T) {
	in := `Phase: Running
RestartPolicy: Always
Containers:
  app:
    Image: nginx:1.25
    Ports: 80/TCP
    Limits:
      cpu: 500m
      memory: 128Mi
Events:
  Normal Scheduled  Successfully assigned default/app to node-1`
	out := redact.Redact(in)
	if out != in {
		t.Errorf("non-secret text was modified.\nin:  %q\nout: %q", in, out)
	}
}
