// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shivangtanwar/kbark/internal/ai"
)

// DefaultMaxTokens caps each turn's response length. 1500 tokens is
// roughly 3-4 short paragraphs — long enough for a thorough explanation
// or a couple of tool calls, short enough that runaway loops are bounded.
const DefaultMaxTokens = 1500

// MaxToolTurns caps the multi-turn tool loop. Plenty for a real
// diagnosis (Anthropic typically converges in 1-2 turns); the bound
// exists to prevent a misbehaving model burning through quota with
// infinite tool calls. Empirically 5 has plenty of headroom.
const MaxToolTurns = 5

// TurnTimeout caps how long a single provider.Stream call may run.
// Without this bound, a stalled SSE connection (Anthropic occasionally
// hangs late in a long conversation) would freeze the diagnose modal
// indefinitely. 60s is well past any reasonable token-generation
// latency for our prompts.
const TurnTimeout = 60 * time.Second

// SystemPrompt is the v1 instruction set we hand the model. Iterated in M9.
const SystemPrompt = `You are an expert Kubernetes operator. The user has selected a pod and pressed "?" for a diagnosis.

Below is the pod's current state: phase, container statuses, recent events, and the tail of its logs. This initial payload is authoritative for the pod itself — do NOT call describe_pod or get_resource on the pod under diagnosis; you already have what you need about it.

Use tools only when the initial payload genuinely lacks something:
- get_previous_logs — to read what a CrashLoopBackOff container printed before its last restart.
- get_events — only if the events shown above seem incomplete.
- get_resource — to inspect a *different* resource the pod references (a ConfigMap, Secret, Service, PVC, etc.), not the pod itself.

Prefer at most one or two tool calls. Once you have the evidence you need, write the final answer.

Final answer rules:
- Two or three short paragraphs of plain prose. No markdown bullets, no headers.
- Be specific about the likely root cause when the data supports it (e.g. "the readiness probe targets port 8081 but the container listens on 8080").
- Cite the evidence ("the events show ImagePullBackOff", "the logs end with panic: ...") rather than asserting facts you can't see.
- If the pod looks healthy, say so plainly and stop.
- Never invent details that aren't in the data. If logs are absent, say "no logs available" instead of speculating.`

// ResourceSystemPrompt is the kind-agnostic variant used for `?` on
// any non-pod resource (deployments, services, configmaps, …). The
// payload header tells the model what kind it's looking at; the
// model adapts its framing accordingly. Kept generic deliberately —
// shipping 11 kind-specific prompts would risk drift between them
// without measurably better answers.
const ResourceSystemPrompt = `You are an expert Kubernetes operator. The user has pressed "?" on a resource for a diagnosis.

The payload contains:
1. A header line naming the resource kind, namespace (if any), and name.
2. The kubectl-style describe output for that resource, which includes its spec, status, and recent events inline.

Anchor your answer on this specific resource. Read the describe output for the relevant signals — for a workload (Deployment, StatefulSet, DaemonSet), look at desired vs ready replicas and rollout status; for a Service, look at type, selectors, endpoints; for a ConfigMap or Secret, look at data keys and any consumer references; for a Node, look at conditions, allocatable resources, and taints.

Tools you have:
- get_events — if you suspect the describe output's events section is incomplete (rare, but possible if the resource emits events that aren't selected by name).
- get_resource — to inspect a *different* resource that this one references (e.g. a Deployment's ReplicaSet, a Service's selector targets, a Node's pods). Don't call describe again on the resource you already have.

Prefer at most one tool call. Most of the time the describe block is enough.

Final answer rules:
- Two or three short paragraphs of plain prose. No markdown bullets, no headers.
- Be specific about what the resource is doing right now (e.g. "the Deployment has 3 desired replicas but only 2 are ready because the most recent rollout's pod is failing readiness").
- Cite evidence from the describe output ("Status shows 0/3 ready", "Events list a FailedScheduling reason of insufficient memory").
- If the resource looks healthy, say so plainly and stop.
- Never invent details that aren't in the data.`

// LogSystemPrompt is the variant used when the user presses "?" on a
// specific log line. The model sees both the pod context block and a
// "Log focus" block marking the cursor line; the answer should be
// anchored on what THAT line means in the context of the pod, not a
// general pod health summary.
const LogSystemPrompt = `You are an expert Kubernetes operator. The user has pressed "?" on a specific log line and wants to understand what it means in the context of the pod.

The payload contains:
1. The pod's current state (phase, container statuses, recent events, tail of logs).
2. A "Log focus" block with the focal line marked by ">" and the surrounding lines for context.

Anchor your answer on the focal line. Explain what that specific line is saying, why it appeared, what (if anything) it indicates is wrong, and what the user should look at next. Use the surrounding window to read the line in context (a single error can mean different things depending on what came before it).

Tools work the same way as for the pod flow — pull more context only when it would meaningfully change the answer.

Final answer rules:
- Two or three short paragraphs of plain prose. No markdown bullets, no headers.
- Quote the focal line at least once verbatim so the user can confirm you're talking about the right thing.
- Be specific: "this line is the application's startup probe failing because…" rather than "this could be a problem with the application".
- If the focal line is benign (info-level message, normal lifecycle output), say so plainly and stop.
- Never invent details that aren't in the data.`

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

// Start opens a streaming session with the default pod-focused system
// prompt. Pass dispatcher = nil to fall back to M5-style one-shot
// behaviour (no tools advertised, no loop).
func Start(
	ctx context.Context,
	provider ai.Provider,
	model, payload string,
	dispatcher *Dispatcher,
) *Session {
	return StartWithPrompt(ctx, provider, model, payload, SystemPrompt, dispatcher)
}

// StartWithPrompt is Start with an explicit system prompt — used by
// the `?`-on-log-line flow to swap in LogSystemPrompt while keeping
// the rest of the session machinery identical.
func StartWithPrompt(
	ctx context.Context,
	provider ai.Provider,
	model, payload, systemPrompt string,
	dispatcher *Dispatcher,
) *Session {
	sessionCtx, cancel := context.WithCancel(ctx)
	out := make(chan ai.Event, 8)
	go runLoop(sessionCtx, provider, model, payload, systemPrompt, dispatcher, out)
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
	model, payload, systemPrompt string,
	dispatcher *Dispatcher,
	out chan<- ai.Event,
) {
	defer close(out)

	var tools []ai.Tool
	if dispatcher != nil {
		tools = dispatcher.Tools()
	}

	messages := []ai.Message{{Role: ai.RoleUser, Content: payload}}
	debugf("runLoop starting tools=%d payload_len=%d", len(tools), len(payload))

	for turn := 0; turn < MaxToolTurns; turn++ {
		// The per-turn timeout is a derived context so any branch out of
		// this loop body via return cancels the timeout cleanly. Defer
		// stacks up to MaxToolTurns levels deep — negligible — and fires
		// when runLoop exits.
		shouldContinue, err := runTurn(ctx, turn, provider, model, systemPrompt, &messages, tools, dispatcher, out)
		if err != nil {
			sendSessionEvent(ctx, out, ai.ErrorEvent{Err: err})
			return
		}
		if !shouldContinue {
			return
		}
	}

	sendSessionEvent(ctx, out, ai.ErrorEvent{Err: ErrMaxToolTurnsExceeded})
}

// runTurn executes one round of the streaming loop. Returns
// (continueLoop, err). When err != nil the caller emits an ErrorEvent.
// When !continueLoop, the caller exits (final answer was already
// streamed and a DoneEvent was emitted from inside the turn).
func runTurn(
	parentCtx context.Context,
	turn int,
	provider ai.Provider,
	model, systemPrompt string,
	messages *[]ai.Message,
	tools []ai.Tool,
	dispatcher *Dispatcher,
	out chan<- ai.Event,
) (bool, error) {
	turnStart := time.Now()
	turnCtx, turnCancel := context.WithTimeout(parentCtx, TurnTimeout)
	defer turnCancel()

	debugf("turn=%d opening stream messages=%d (timeout=%s)", turn, len(*messages), TurnTimeout)
	innerEvents, err := provider.Stream(turnCtx, ai.Request{
		Model:     model,
		System:    systemPrompt,
		Messages:  *messages,
		MaxTokens: DefaultMaxTokens,
		Tools:     tools,
	})
	if err != nil {
		debugf("turn=%d Stream() returned err=%v", turn, err)
		return false, err
	}

	var (
		assistantText strings.Builder
		pending       []ai.ToolCallEvent
		stopReason    string
		eventCount    int
	)
	// Read events via select instead of for-range so we honour the
	// per-turn timeout even when the provider goroutine is stuck inside
	// the SDK's stream.Next() and doesn't close the events channel.
	// Sends to `out` also race against turnCtx — without that the
	// session would block here waiting for the consumer's buffer space
	// even after the timeout has expired.
streamLoop:
	for {
		select {
		case ev, ok := <-innerEvents:
			if !ok {
				break streamLoop
			}
			eventCount++
			switch e := ev.(type) {
			case ai.TextDeltaEvent:
				assistantText.WriteString(e.Delta)
				if !forwardEvent(turnCtx, out, e) {
					debugf("turn=%d forwardEvent(TextDelta) cancelled after events=%d (turnCtx.Err=%v)",
						turn, eventCount, turnCtx.Err())
					if turnCtx.Err() == context.DeadlineExceeded {
						return false, errors.New("turn timed out after " + TurnTimeout.String())
					}
					return false, nil
				}
			case ai.ToolCallEvent:
				debugf("turn=%d tool_call name=%s id=%s args=%s", turn, e.Name, e.ID, e.Arguments)
				pending = append(pending, e)
				if !forwardEvent(turnCtx, out, e) {
					return false, nil
				}
			case ai.DoneEvent:
				stopReason = e.StopReason
				debugf("turn=%d done stop_reason=%q after events=%d", turn, stopReason, eventCount)
			case ai.ErrorEvent:
				debugf("turn=%d error from provider: %v", turn, e.Err)
				forwardEvent(turnCtx, out, e)
				return false, nil
			}
		case <-turnCtx.Done():
			debugf("turn=%d turnCtx fired (%v) after events=%d; abandoning provider goroutine",
				turn, turnCtx.Err(), eventCount)
			if turnCtx.Err() == context.DeadlineExceeded {
				return false, errors.New("turn timed out after " + TurnTimeout.String())
			}
			// Parent cancellation — propagate as a clean exit, not an error.
			return false, nil
		}
	}
	debugf("turn=%d inner stream closed events=%d pending_tools=%d text_len=%d elapsed=%v",
		turn, eventCount, len(pending), assistantText.Len(), time.Since(turnStart))

	// No tool calls this turn — model has produced its final answer.
	if len(pending) == 0 || dispatcher == nil {
		debugf("turn=%d exiting via DoneEvent (no tools)", turn)
		sendSessionEvent(parentCtx, out, ai.DoneEvent{StopReason: stopReason})
		return false, nil
	}

	// Build assistant turn (text + tool_uses) and the user turn that
	// follows with the tool results.
	results := make([]ai.ToolResult, 0, len(pending))
	for i, tc := range pending {
		dispatchStart := time.Now()
		content, derr := dispatcher.Dispatch(parentCtx, tc)
		debugf("turn=%d tool[%d]=%s elapsed=%v err=%v content_len=%d",
			turn, i, tc.Name, time.Since(dispatchStart), derr, len(content))
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

	*messages = append(
		*messages,
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
	debugf("turn=%d appended assistant+user_tool_results; next turn starts with %d messages",
		turn, len(*messages))
	return true, nil
}

func sendSessionEvent(ctx context.Context, out chan<- ai.Event, e ai.Event) bool {
	select {
	case out <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// forwardEvent is like sendSessionEvent but bails on a turn-scoped ctx
// instead of the long-lived session ctx. Used inside the per-turn loop
// so that a per-turn deadline cuts off in-flight sends to a slow
// consumer; the long-lived parentCtx is only respected at end-of-loop
// (DoneEvent / final ErrorEvent forwarding from runLoop).
func forwardEvent(turnCtx context.Context, out chan<- ai.Event, e ai.Event) bool {
	select {
	case out <- e:
		return true
	case <-turnCtx.Done():
		return false
	}
}
