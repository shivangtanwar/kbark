// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"

	"github.com/shivangtanwar/kbark/internal/ai"
)

// DefaultMaxTokens caps the diagnosis response length. 1500 tokens is
// roughly 3-4 short paragraphs — long enough for a thorough explanation,
// short enough that the user can read it without scrolling much.
const DefaultMaxTokens = 1500

// SystemPrompt is the v1 instruction set we hand the model. Iterated in M9.
const SystemPrompt = `You are an expert Kubernetes operator. The user has selected a pod and pressed "?" for a diagnosis.

Below is the pod's current state: phase, container statuses, recent events, and the tail of its logs.

Your job:
- Identify what is wrong, in 2 to 3 short paragraphs.
- Be specific about the likely root cause when the data supports it (e.g. "the readiness probe targets port 8081 but the container listens on 8080").
- Cite the evidence ("the events show ImagePullBackOff", "the logs end with panic: ...") rather than asserting facts you can't see.
- If the pod looks healthy, say so plainly and stop.
- Never invent details that aren't in the data. If logs are absent, say "no logs available" instead of speculating.

Write in plain text. No markdown bullets, no headers. Two or three paragraphs of prose.`

// Session is a one-shot diagnosis stream. The caller selects on Events()
// until the channel closes; Cancel() (or a parent ctx) shuts the stream
// down within ~200ms (provider-dependent).
type Session struct {
	cancel context.CancelFunc
	events <-chan ai.Event
}

// Start opens a streaming session against the provider with the assembled
// context as the user message. The returned Session begins streaming
// immediately; the caller should drain Events() until close.
//
// If provider.Stream returns an error synchronously, Start returns a
// Session whose Events() channel emits a single ErrorEvent and closes.
// This keeps the consumer's code path uniform (always range over events).
func Start(ctx context.Context, provider ai.Provider, model, payload string) *Session {
	sessionCtx, cancel := context.WithCancel(ctx)
	events, err := provider.Stream(sessionCtx, ai.Request{
		Model:     model,
		System:    SystemPrompt,
		Messages:  []ai.Message{{Role: ai.RoleUser, Content: payload}},
		MaxTokens: DefaultMaxTokens,
	})
	if err != nil {
		cancel()
		errCh := make(chan ai.Event, 1)
		errCh <- ai.ErrorEvent{Err: err}
		close(errCh)
		return &Session{cancel: func() {}, events: errCh}
	}
	return &Session{cancel: cancel, events: events}
}

// Events returns the channel of streaming events; closes when the
// provider's stream ends (Done, Error, or context cancellation).
func (s *Session) Events() <-chan ai.Event { return s.events }

// Cancel terminates the stream. Safe to call multiple times — the second
// call is a no-op on the underlying context.
func (s *Session) Cancel() { s.cancel() }
