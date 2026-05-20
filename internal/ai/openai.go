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
	var stopReason string

	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
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

	if err := stream.Err(); err != nil {
		sendEvent(ctx, out, ErrorEvent{Err: classifyOpenAIError(err)})
		return
	}
	sendEvent(ctx, out, DoneEvent{StopReason: stopReason})
}

func buildOpenAIMessages(system string, in []Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(in)+1)
	if system != "" {
		out = append(out, openai.SystemMessage(system))
	}
	for _, m := range in {
		switch m.Role {
		case RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case RoleAssistant:
			out = append(out, openai.AssistantMessage(m.Content))
		}
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
