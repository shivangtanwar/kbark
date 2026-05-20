// SPDX-License-Identifier: Apache-2.0

package ai

import "fmt"

// RateLimitError indicates the provider declined the request because of
// quota or rate limiting. The TUI surfaces this with a hint to switch
// profiles or fall back to a local model.
type RateLimitError struct {
	Provider string
	Detail   string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s rate-limited: %s", e.Provider, e.Detail)
}

// AuthError indicates an invalid or missing API key.
type AuthError struct {
	Provider string
	Detail   string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("%s auth failed: %s", e.Provider, e.Detail)
}

// MissingEnvError is returned by provider constructors when their
// required env var (ANTHROPIC_API_KEY, OPENAI_API_KEY, …) is unset.
type MissingEnvError struct {
	Provider string
	EnvVar   string
}

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("%s requires %s to be set", e.Provider, e.EnvVar)
}
