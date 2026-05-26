// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/shivangtanwar/kbark/internal/ai"
)

// TestWaitForDiagnoseEvent_ToolCallReturnsMessage pins the regression that
// wedged M6: a ToolCallEvent must translate into a real bubbletea message,
// not nil. A nil return ends the Cmd without anyone re-issuing
// waitForDiagnoseEvent, so the pump dies, the session's buffered channel
// fills, and the diagnosis hangs with no error ever surfacing.
func TestWaitForDiagnoseEvent_ToolCallReturnsMessage(t *testing.T) {
	ch := make(chan ai.Event, 1)
	ch <- ai.ToolCallEvent{Name: "get_previous_logs", ID: "t1"}

	msg := waitForDiagnoseEvent(ch)()

	tc, ok := msg.(DiagnosisToolCallMsg)
	if !ok {
		t.Fatalf("expected DiagnosisToolCallMsg, got %T (%v)", msg, msg)
	}
	if tc.Name != "get_previous_logs" {
		t.Fatalf("tool name = %q, want %q", tc.Name, "get_previous_logs")
	}
}

// TestUpdate_ToolCallKeepsPumpAlive proves the Model re-issues the events
// pump when it handles a tool call. The returned Cmd is the next read off
// the channel; if it were nil the stream would stall the moment a tool
// fires (the exact M6 hang).
func TestUpdate_ToolCallKeepsPumpAlive(t *testing.T) {
	m := Model{diagnoseEventsCh: make(chan ai.Event)}

	_, cmd := m.Update(DiagnosisToolCallMsg{Name: "get_events"})

	if cmd == nil {
		t.Fatal("Update(DiagnosisToolCallMsg) returned nil Cmd; pump would stall")
	}
}

// TestDiagnoseEventLoop_DrainsToolCallsToCompletion runs the full Cmd loop
// over a representative event sequence — text, a tool call, more text, then
// Done — and asserts every event is consumed and the loop terminates with a
// DiagnosisDoneMsg. Before the fix, the tool call returned nil and the loop
// halted with the trailing text and DoneEvent still unread.
func TestDiagnoseEventLoop_DrainsToolCallsToCompletion(t *testing.T) {
	events := []ai.Event{
		ai.TextDeltaEvent{Delta: "Let me check the previous logs. "},
		ai.ToolCallEvent{Name: "get_previous_logs", ID: "t1"},
		ai.TextDeltaEvent{Delta: "The container OOMed."},
		ai.DoneEvent{StopReason: "end_turn"},
	}
	ch := make(chan ai.Event, len(events))
	for _, e := range events {
		ch <- e
	}

	m := tea.Model(Model{diagnoseEventsCh: ch})

	var (
		text     string
		toolSeen bool
		done     bool
	)

	// Prime the loop the way DiagnosisStartedMsg would, then run until a
	// terminal message. A bound prevents a hang from masquerading as a pass.
	cmd := waitForDiagnoseEvent(ch)
	for i := 0; i < 100 && cmd != nil && !done; i++ {
		msg := cmd()
		var next tea.Cmd
		m, next = m.Update(msg)
		switch v := msg.(type) {
		case DiagnosisDeltaMsg:
			text += v.Text
		case DiagnosisToolCallMsg:
			toolSeen = true
		case DiagnosisDoneMsg:
			done = true
		case DiagnosisErrorMsg:
			t.Fatalf("unexpected error msg: %v", v.Err)
		}
		cmd = next
	}

	if !toolSeen {
		t.Error("tool call was never surfaced as a message")
	}
	if !done {
		t.Fatal("loop did not reach DiagnosisDoneMsg — pump stalled")
	}
	if want := "Let me check the previous logs. The container OOMed."; text != want {
		t.Errorf("accumulated text = %q, want %q", text, want)
	}
}
