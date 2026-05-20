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

// pendingToolUse accumulates a tool_use block's incremental input JSON as
// ContentBlockDeltaEvent / InputJSONDelta events arrive. The fully-formed
// ToolCallEvent is emitted on ContentBlockStopEvent for that block.
type pendingToolUse struct {
	id        string
	name      string
	inputJSON strings.Builder
}

func (p *AnthropicProvider) runStream(
	ctx context.Context,
	params anthropic.MessageNewParams,
	out chan<- Event,
) {
	defer close(out)

	stream := p.client.Messages.NewStreaming(ctx, params)
	var stopReason string
	var current *pendingToolUse

	for stream.Next() {
		ev := stream.Current()
		switch evt := ev.AsAny().(type) {

		case anthropic.MessageDeltaEvent:
			if string(evt.Delta.StopReason) != "" {
				stopReason = string(evt.Delta.StopReason)
			}

		case anthropic.ContentBlockStartEvent:
			if tu, ok := evt.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
				current = &pendingToolUse{id: tu.ID, name: tu.Name}
			}

		case anthropic.ContentBlockDeltaEvent:
			switch delta := evt.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if !sendEvent(ctx, out, TextDeltaEvent{Delta: delta.Text}) {
					return
				}
			case anthropic.InputJSONDelta:
				if current != nil {
					current.inputJSON.WriteString(delta.PartialJSON)
				}
			}

		case anthropic.ContentBlockStopEvent:
			if current != nil {
				args := current.inputJSON.String()
				if args == "" {
					args = "{}"
				}
				if !sendEvent(ctx, out, ToolCallEvent{
					ID:        current.id,
					Name:      current.name,
					Arguments: args,
				}) {
					return
				}
				current = nil
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
