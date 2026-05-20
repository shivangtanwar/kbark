// SPDX-License-Identifier: Apache-2.0

// Package ai is the provider-agnostic streaming interface kbark uses to
// talk to language models. Concrete providers (Anthropic, OpenAI, Ollama)
// live in sibling files and all satisfy the same Provider contract.
//
// The shape is deliberately small: one Stream call returns a channel of
// Events. The consumer reads until the channel closes. Cancellation goes
// through ctx, never through a separate Stop method — one fewer state
// machine to coordinate.
package ai

import "context"

// Provider streams a completion as a sequence of Events.
type Provider interface {
	// Name returns the canonical provider identifier (e.g. "anthropic").
	Name() string
	// Stream issues the request and returns a channel of Events. The
	// channel is closed by the provider goroutine when the response ends
	// (DoneEvent or ErrorEvent emitted, then close). Cancelling ctx
	// causes the goroutine to drain quickly and close the channel.
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// Request is the provider-agnostic input.
type Request struct {
	// Model is a provider-specific model identifier (e.g.
	// "claude-sonnet-4-6", "gpt-4.1-mini", "llama3.1:8b-instruct").
	Model string
	// System is the system prompt; empty means provider default.
	System string
	// Messages is the conversation so far. Assistant turns may carry
	// ToolUses (the model's previous tool calls) and user turns may
	// carry ToolResults (responses we generated to those calls).
	Messages []Message
	// MaxTokens caps the response length. Zero means provider default.
	MaxTokens int
	// Tools, if non-empty, advertises function-call capability to the
	// model. Tool calls land in the stream as ToolCallEvent. Used by M6.
	Tools []Tool
}

// Role identifies the speaker for a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in the conversation. A turn may carry plain text
// content, a list of tool_use blocks (assistant role: the model's
// outgoing tool calls), and/or a list of tool_result blocks (user role:
// the responses we produced for those tool calls). Providers translate
// this into their own native multi-part-message representation.
type Message struct {
	Role        Role
	Content     string
	ToolUses    []ToolCallEvent
	ToolResults []ToolResult
}

// Tool advertises a function the model may call.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON schema for the tool's input. Use map[string]any
	// so providers can serialize without us caring about their preferred
	// representation (Anthropic and OpenAI both accept this shape).
	Schema map[string]any
}

// ToolResult is the response to a tool call from a previous assistant turn.
type ToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

// Event is one streaming event. The sealed-interface pattern (private
// isAIEvent method) makes the set closed; consumers use a type switch.
type Event interface {
	isAIEvent()
}

// TextDeltaEvent is a chunk of generated text. Multiple deltas arrive
// before the assistant's turn ends.
type TextDeltaEvent struct {
	Delta string
}

// ToolCallEvent is the model requesting a tool invocation. ID is unique
// per call within a stream; the caller echoes it back in a ToolResult.
type ToolCallEvent struct {
	ID        string
	Name      string
	Arguments string // JSON-encoded per the tool's Schema
}

// DoneEvent indicates the assistant's turn is over. StopReason is the
// provider's own field, normalized when straightforward. For Anthropic
// the value "tool_use" signals the model wants to use tools; the
// session loop should dispatch them and call Stream again.
type DoneEvent struct {
	StopReason string
}

// ErrorEvent surfaces a non-fatal mid-stream error (rate limit, network
// blip). After an ErrorEvent the channel closes.
type ErrorEvent struct {
	Err error
}

func (TextDeltaEvent) isAIEvent() {}
func (ToolCallEvent) isAIEvent()  {}
func (DoneEvent) isAIEvent()      {}
func (ErrorEvent) isAIEvent()     {}

// StopReasonToolUse is the canonical value Anthropic emits when the
// model wants to invoke tools. We surface it as-is so the session loop
// can branch on it cleanly.
const StopReasonToolUse = "tool_use"
