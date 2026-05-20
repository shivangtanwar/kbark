// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"encoding/json"
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
	if len(req.Tools) > 0 {
		params.Tools = buildAnthropicTools(req.Tools)
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

	// message accumulates ALL stream events into the final assembled
	// response. After the stream closes, message.Content holds the
	// finished text and tool_use blocks regardless of how they were
	// chunked across SDK events — far more reliable than trying to
	// type-switch on ContentBlockStartEvent / ContentBlockStopEvent
	// (whose exact shape varies across SDK versions).
	message := anthropic.Message{}

	for stream.Next() {
		ev := stream.Current()
		if err := message.Accumulate(ev); err != nil {
			// Accumulation failures are non-fatal for the user; the
			// stream itself can keep running.
			continue
		}
		// Forward only text deltas in real time; tool calls are emitted
		// once at end-of-stream from the accumulated message.
		if evt, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
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

	// Emit one ToolCallEvent per tool_use block in the assembled message.
	for _, block := range message.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			args := marshalToolInput(tu.Input)
			if !sendEvent(ctx, out, ToolCallEvent{
				ID:        tu.ID,
				Name:      tu.Name,
				Arguments: args,
			}) {
				return
			}
		}
	}
	sendEvent(ctx, out, DoneEvent{StopReason: string(message.StopReason)})
}

// marshalToolInput turns an arbitrary input value back into a JSON string
// for our cross-provider ToolCallEvent.Arguments contract. Failing back
// to "{}" keeps downstream consumers from choking on a malformed call.
func marshalToolInput(input any) string {
	if input == nil {
		return "{}"
	}
	if raw, ok := input.(json.RawMessage); ok && len(raw) > 0 {
		return string(raw)
	}
	if buf, err := json.Marshal(input); err == nil && len(buf) > 0 {
		return string(buf)
	}
	return "{}"
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
		blocks := buildAnthropicBlocks(m)
		if len(blocks) == 0 {
			continue
		}
		switch m.Role {
		case RoleUser:
			out = append(out, anthropic.NewUserMessage(blocks...))
		case RoleAssistant:
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		}
	}
	return out
}

func buildAnthropicBlocks(m Message) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolUses)+len(m.ToolResults))
	if m.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	for _, tu := range m.ToolUses {
		var input any
		if tu.Arguments != "" {
			_ = json.Unmarshal([]byte(tu.Arguments), &input)
		}
		if input == nil {
			input = map[string]any{}
		}
		blocks = append(blocks, anthropic.NewToolUseBlock(tu.ID, input, tu.Name))
	}
	for _, tr := range m.ToolResults {
		blocks = append(blocks, anthropic.NewToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
	}
	return blocks
}

func buildAnthropicTools(in []Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, len(in))
	for i, t := range in {
		props, _ := t.Schema["properties"].(map[string]any)
		out[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Type:       "object",
					Properties: props,
					Required:   extractRequiredFromSchema(t.Schema),
				},
			},
		}
	}
	return out
}

// extractRequiredFromSchema pulls the "required" key out of a JSON schema
// map. Schemas authored as Go literals tend to declare it as []string,
// but Schemas round-tripped through json.Unmarshal end up as []any.
// Both shapes need to work.
func extractRequiredFromSchema(schema map[string]any) []string {
	switch r := schema["required"].(type) {
	case []string:
		return r
	case []any:
		out := make([]string, 0, len(r))
		for _, v := range r {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// classifyAnthropicError promotes well-known errors to typed forms so the
// TUI can branch on them. Falls back to the raw error for anything else.
func classifyAnthropicError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "429"), strings.Contains(strings.ToLower(msg), "rate_limit"):
		return &RateLimitError{Provider: "anthropic", Detail: msg}
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"),
		strings.Contains(strings.ToLower(msg), "authentication"):
		return &AuthError{Provider: "anthropic", Detail: msg}
	}
	return err
}
