// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"errors"
	"strings"

	"github.com/shivangtanwar/kbark/internal/ai"
)

// DefaultMaxTokens caps each turn's response length. 1500 tokens is
// roughly 3-4 short paragraphs — long enough for a thorough explanation
// or a couple of tool calls, short enough that runaway loops are bounded.
const DefaultMaxTokens = 1500

// MaxToolTurns caps the multi-turn tool loop. Plenty for a real
// diagnosis (Anthropic typically converges in 1-3 turns); the bound
// exists to prevent a misbehaving model burning through quota with
// infinite tool calls.
const MaxToolTurns = 10

// SystemPrompt is the v1 instruction set we hand the model. Iterated in M9.
const SystemPrompt = `You are an expert Kubernetes operator. The user has selected a pod and pressed "?" for a diagnosis.

Below is the pod's current state: phase, container statuses, recent events, and the tail of its logs.

When you have access to tools, use them to gather additional context as needed:
- get_events / get_previous_logs / get_logs are useful for CrashLoopBackOff and image-pull failures
- describe_pod helps when you need the pod's spec (mounts, probes)
- get_resource lets you inspect a ConfigMap or Secret the pod references

Your final job:
- Identify what is wrong, in 2 to 3 short paragraphs.
- Be specific about the likely root cause when the data supports it (e.g. "the readiness probe targets port 8081 but the container listens on 8080").
- Cite the evidence ("the events show ImagePullBackOff", "the logs end with panic: ...") rather than asserting facts you can't see.
- If the pod looks healthy, say so plainly and stop.
- Never invent details that aren't in the data. If logs are absent, say "no logs available" instead of speculating.

Write the final answer in plain text. No markdown bullets, no headers. Two or three paragraphs of prose.`

// ErrMaxToolTurnsExceeded fires when the model keeps calling tools past
// the MaxToolTurns cap. Surfaced as an ErrorEvent to the consumer.
var ErrMaxToolTurnsExceeded = errors.New("diagnosis exceeded maximum tool-call turns")

// Session is the multi-turn streaming AI session for one `?` press.
//
// When dispatcher is non-nil and the provider supports tool calls,
// Session loops: stream → collect tool calls → dispatch → continue
// the conversation with results → repeat. The consumer reads Events()
// without needing to know about the loop; tool calls surface as
// ToolCallEvents (for UI breadcrumbs), text as TextDeltaEvents, and
// final completion as a single DoneEvent.
//
// Cancel() (or parent ctx) terminates the in-flight stream and ends
// any pending tool dispatch within ~200ms.
type Session struct {
	cancel context.CancelFunc
	events <-chan ai.Event
}

// Start opens a streaming session. Pass dispatcher = nil to fall back to
// M5-style one-shot behaviour (no tools advertised, no loop).
func Start(
	ctx context.Context,
	provider ai.Provider,
	model, payload string,
	dispatcher *Dispatcher,
) *Session {
	sessionCtx, cancel := context.WithCancel(ctx)
	out := make(chan ai.Event, 8)
	go runLoop(sessionCtx, provider, model, payload, dispatcher, out)
	return &Session{cancel: cancel, events: out}
}

// Events returns the channel of streaming events for the lifetime of
// the session. Closes when the conversation is over (DoneEvent or
// ErrorEvent emitted then close).
func (s *Session) Events() <-chan ai.Event { return s.events }

// Cancel terminates the session. Safe to call multiple times.
func (s *Session) Cancel() { s.cancel() }

// runLoop is the core multi-turn loop. It owns the conversation state
// (`messages`) and the channel close on exit.
func runLoop(
	ctx context.Context,
	provider ai.Provider,
	model, payload string,
	dispatcher *Dispatcher,
	out chan<- ai.Event,
) {
	defer close(out)

	var tools []ai.Tool
	if dispatcher != nil {
		tools = dispatcher.Tools()
	}

	messages := []ai.Message{{Role: ai.RoleUser, Content: payload}}

	for turn := 0; turn < MaxToolTurns; turn++ {
		innerEvents, err := provider.Stream(ctx, ai.Request{
			Model:     model,
			System:    SystemPrompt,
			Messages:  messages,
			MaxTokens: DefaultMaxTokens,
			Tools:     tools,
		})
		if err != nil {
			sendSessionEvent(ctx, out, ai.ErrorEvent{Err: err})
			return
		}

		var (
			assistantText strings.Builder
			pending       []ai.ToolCallEvent
			stopReason    string
		)
		for ev := range innerEvents {
			switch e := ev.(type) {
			case ai.TextDeltaEvent:
				assistantText.WriteString(e.Delta)
				if !sendSessionEvent(ctx, out, e) {
					return
				}
			case ai.ToolCallEvent:
				pending = append(pending, e)
				if !sendSessionEvent(ctx, out, e) {
					return
				}
			case ai.DoneEvent:
				stopReason = e.StopReason
			case ai.ErrorEvent:
				sendSessionEvent(ctx, out, e)
				return
			}
		}

		// No tool calls this turn — model has produced its final answer.
		if len(pending) == 0 || dispatcher == nil {
			sendSessionEvent(ctx, out, ai.DoneEvent{StopReason: stopReason})
			return
		}

		// Build assistant turn (text + tool_uses) and the user turn that
		// follows with the tool results.
		results := make([]ai.ToolResult, 0, len(pending))
		for _, tc := range pending {
			content, derr := dispatcher.Dispatch(ctx, tc)
			isErr := derr != nil
			if isErr {
				content = derr.Error()
			}
			results = append(results, ai.ToolResult{
				ToolCallID: tc.ID,
				Content:    content,
				IsError:    isErr,
			})
		}

		messages = append(
			messages,
			ai.Message{
				Role:     ai.RoleAssistant,
				Content:  assistantText.String(),
				ToolUses: pending,
			},
			ai.Message{
				Role:        ai.RoleUser,
				ToolResults: results,
			},
		)
	}

	sendSessionEvent(ctx, out, ai.ErrorEvent{Err: ErrMaxToolTurnsExceeded})
}

func sendSessionEvent(ctx context.Context, out chan<- ai.Event, e ai.Event) bool {
	select {
	case out <- e:
		return true
	case <-ctx.Done():
		return false
	}
}
