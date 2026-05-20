// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// AnthropicProvider streams Messages API responses from api.anthropic.com.
// The SDK reads the API key from ANTHROPIC_API_KEY automatically; we only
// gate construction on the env var being present.
type AnthropicProvider struct {
	client anthropic.Client
}

// NewAnthropic constructs the provider. Returns MissingEnvError if the
// API key isn't set so the caller can surface a useful row in `kbark doctor`.
func NewAnthropic() (*AnthropicProvider, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, &MissingEnvError{Provider: "anthropic", EnvVar: "ANTHROPIC_API_KEY"}
	}
	return &AnthropicProvider{client: anthropic.NewClient()}, nil
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	params := anthropic.MessageNewParams{
		MaxTokens: int64(req.MaxTokens),
		Model:     anthropic.Model(req.Model),
		Messages:  buildAnthropicMessages(req.Messages),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	events := make(chan Event, 8)
	go p.runStream(ctx, params, events)
	return events, nil
}

func (p *AnthropicProvider) runStream(
	ctx context.Context,
	params anthropic.MessageNewParams,
	out chan<- Event,
) {
	defer close(out)

	stream := p.client.Messages.NewStreaming(ctx, params)
	var stopReason string

	for stream.Next() {
		ev := stream.Current()
		switch evt := ev.AsAny().(type) {
		case anthropic.MessageDeltaEvent:
			if string(evt.Delta.StopReason) != "" {
				stopReason = string(evt.Delta.StopReason)
			}
		case anthropic.ContentBlockDeltaEvent:
			if delta, ok := evt.Delta.AsAny().(anthropic.TextDelta); ok {
				if !sendEvent(ctx, out, TextDeltaEvent{Delta: delta.Text}) {
					return
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		sendEvent(ctx, out, ErrorEvent{Err: classifyAnthropicError(err)})
		return
	}
	sendEvent(ctx, out, DoneEvent{StopReason: stopReason})
}

// sendEvent guards every send on ctx so a cancelled consumer doesn't
// block the goroutine indefinitely. Returns false if ctx is done.
func sendEvent(ctx context.Context, out chan<- Event, e Event) bool {
	select {
	case out <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

func buildAnthropicMessages(in []Message) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(in))
	for _, m := range in {
		switch m.Role {
		case RoleUser:
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			out = append(out, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		}
	}
	return out
}

// classifyAnthropicError promotes well-known errors to typed forms so the
// TUI can branch on them. Falls back to the raw error for anything else.
func classifyAnthropicError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// The SDK's error message includes the HTTP status. Cheap classification
	// via substring is robust enough for our use; the alternative would be
	// type-asserting on the SDK's internal error type which is more brittle
	// across SDK upgrades.
	switch {
	case strings.Contains(msg, "429"), strings.Contains(strings.ToLower(msg), "rate_limit"):
		return &RateLimitError{Provider: "anthropic", Detail: msg}
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"),
		strings.Contains(strings.ToLower(msg), "authentication"):
		return &AuthError{Provider: "anthropic", Detail: msg}
	}
	return err
}
