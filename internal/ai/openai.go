// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
)

// OpenAIProvider streams Chat Completions responses from api.openai.com.
// The SDK reads the API key from OPENAI_API_KEY automatically.
type OpenAIProvider struct {
	client openai.Client
}

func NewOpenAI() (*OpenAIProvider, error) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil, &MissingEnvError{Provider: "openai", EnvVar: "OPENAI_API_KEY"}
	}
	return &OpenAIProvider{client: openai.NewClient()}, nil
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(req.Model),
		Messages: buildOpenAIMessages(req.System, req.Messages),
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}
	if len(req.Tools) > 0 {
		params.Tools = buildOpenAITools(req.Tools)
	}

	events := make(chan Event, 8)
	go p.runStream(ctx, params, events)
	return events, nil
}

func (p *OpenAIProvider) runStream(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	out chan<- Event,
) {
	defer close(out)

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	acc := openai.ChatCompletionAccumulator{}
	var stopReason string

	for stream.Next() {
		if ctx.Err() != nil {
			return
		}
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta.Content != "" {
				if !sendEvent(ctx, out, TextDeltaEvent{Delta: choice.Delta.Content}) {
					return
				}
			}
			if choice.FinishReason != "" {
				stopReason = string(choice.FinishReason)
			}
		}

		// JustFinishedToolCall fires exactly once per tool call as soon
		// as the accumulator has the complete name+arguments. Emit our
		// ToolCallEvent here so the UI breadcrumb appears immediately.
		if tool, ok := acc.JustFinishedToolCall(); ok {
			id := toolCallIDFor(&acc, tool.Index)
			if !sendEvent(ctx, out, ToolCallEvent{
				ID:        id,
				Name:      tool.Name,
				Arguments: tool.Arguments,
			}) {
				return
			}
		}
	}

	if err := stream.Err(); err != nil {
		sendEvent(ctx, out, ErrorEvent{Err: classifyOpenAIError(err)})
		return
	}
	sendEvent(ctx, out, DoneEvent{StopReason: stopReason})
}

// toolCallIDFor pulls the canonical OpenAI tool-call ID for the given
// index out of the accumulator's running snapshot. JustFinishedToolCall
// returns name+args but doesn't directly expose ID across SDK versions;
// reading it off the accumulator's snapshot is the stable path.
func toolCallIDFor(acc *openai.ChatCompletionAccumulator, index int) string {
	if len(acc.Choices) == 0 {
		return ""
	}
	for _, tc := range acc.Choices[0].Message.ToolCalls {
		if tc.Function.Name != "" && tc.ID != "" {
			// Match by index when the SDK populates it; otherwise the
			// first ID is correct for index 0.
			if index < 0 || index == 0 {
				return tc.ID
			}
			index--
		}
	}
	return ""
}

func buildOpenAIMessages(system string, in []Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(in)+1)
	if system != "" {
		out = append(out, openai.SystemMessage(system))
	}
	for _, m := range in {
		switch m.Role {
		case RoleUser:
			// In OpenAI's model, tool results are separate "tool" role
			// messages, not part of the user turn. Emit them first, then
			// any text that accompanied the user turn (typically none in
			// kbark's diagnose flow).
			for _, tr := range m.ToolResults {
				out = append(out, openai.ToolMessage(tr.Content, tr.ToolCallID))
			}
			if m.Content != "" {
				out = append(out, openai.UserMessage(m.Content))
			}
		case RoleAssistant:
			out = append(out, buildOpenAIAssistantMessage(m))
		}
	}
	return out
}

// buildOpenAIAssistantMessage constructs the assistant turn, which may
// carry text content, tool calls, or both.
func buildOpenAIAssistantMessage(m Message) openai.ChatCompletionMessageParamUnion {
	if len(m.ToolUses) == 0 {
		// Plain text assistant turn.
		return openai.AssistantMessage(m.Content)
	}
	// Build the union by hand so we can attach ToolCalls.
	assistant := openai.ChatCompletionAssistantMessageParam{}
	if m.Content != "" {
		assistant.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
			OfString: openai.String(m.Content),
		}
	}
	assistant.ToolCalls = make([]openai.ChatCompletionMessageToolCallUnionParam, len(m.ToolUses))
	for i, tu := range m.ToolUses {
		assistant.ToolCalls[i] = openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tu.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tu.Name,
					Arguments: tu.Arguments,
				},
			},
		}
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
}

func buildOpenAITools(in []Tool) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, len(in))
	for i, t := range in {
		out[i] = openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  openai.FunctionParameters(t.Schema),
		})
	}
	return out
}

func classifyOpenAIError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "429"), strings.Contains(low, "rate limit"):
		return &RateLimitError{Provider: "openai", Detail: msg}
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"),
		strings.Contains(low, "incorrect api key"),
		strings.Contains(low, "authentication"):
		return &AuthError{Provider: "openai", Detail: msg}
	}
	return err
}
