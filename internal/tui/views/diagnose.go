// SPDX-License-Identifier: Apache-2.0

package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// DiagnoseState tracks the streaming lifecycle. The view renders slightly
// differently depending on whether more text is still expected.
type DiagnoseState int

const (
	DiagnoseStreaming DiagnoseState = iota
	DiagnoseDone
	DiagnoseErrored
)

// DiagnoseView renders a streaming AI diagnosis. Text accumulates into a
// single string field; the viewport scrolls; we auto-scroll to bottom
// while streaming, and freeze on the user's chosen scroll position once
// Done.
//
// The text buffer is a plain string (not a strings.Builder) because the
// view is passed by value through the bubbletea Update loop — a Builder
// would panic ("illegal use of non-zero Builder copied by value") the
// moment Go's escape analysis chose a different memory location for the
// copy.
type DiagnoseView struct {
	vp    viewport.Model
	text  string
	title string
	state DiagnoseState
	err   error
	th    theme.Theme
}

func NewDiagnoseView(th theme.Theme) DiagnoseView {
	return DiagnoseView{
		vp:    viewport.New(0, 0),
		state: DiagnoseStreaming,
		th:    th,
	}
}

// SetTitle sets the one-line header ("namespace/pod • diagnosing" etc).
func (v DiagnoseView) SetTitle(title string) DiagnoseView {
	v.title = title
	return v
}

func (v DiagnoseView) SetSize(width, height int) DiagnoseView {
	v.vp.Width = width
	titleH, statusH := 0, 0
	if v.title != "" {
		titleH = 1
	}
	if v.state != DiagnoseStreaming || v.err != nil {
		statusH = 1
	}
	inner := height - titleH - statusH
	if inner < 0 {
		inner = 0
	}
	v.vp.Height = inner
	return v
}

// Reset clears the buffer and returns the view to the streaming state.
// Called when the user re-issues `?` on a different (or the same) pod.
func (v DiagnoseView) Reset() DiagnoseView {
	v.text = ""
	v.vp.SetContent("")
	v.state = DiagnoseStreaming
	v.err = nil
	return v
}

// AppendText accumulates a delta and scrolls to bottom (auto-follow
// behaviour while streaming).
func (v DiagnoseView) AppendText(delta string) DiagnoseView {
	v.text += delta
	v.vp.SetContent(v.text)
	if v.state == DiagnoseStreaming {
		v.vp.GotoBottom()
	}
	return v
}

// MarkDone flips the state to "stream finished". The user can still
// scroll the viewport; the view stays open until they press Esc.
func (v DiagnoseView) MarkDone() DiagnoseView {
	v.state = DiagnoseDone
	return v
}

// MarkError flips the state to "errored" and records the error. The
// view renders the error in the status line; any partial text already
// accumulated is preserved above.
func (v DiagnoseView) MarkError(err error) DiagnoseView {
	v.state = DiagnoseErrored
	v.err = err
	return v
}

// State exposes the lifecycle for the parent Model's footer help text.
func (v DiagnoseView) State() DiagnoseState { return v.state }

func (v DiagnoseView) Update(msg tea.Msg) (DiagnoseView, tea.Cmd) {
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v DiagnoseView) View() string {
	parts := make([]string, 0, 3)
	if v.title != "" {
		parts = append(parts, v.th.FooterAccent.Render(v.title))
	}
	parts = append(parts, v.vp.View())
	if status := v.renderStatus(); status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, "\n")
}

func (v DiagnoseView) renderStatus() string {
	switch v.state {
	case DiagnoseDone:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("— diagnosis complete; esc to dismiss —")
	case DiagnoseErrored:
		msg := "(no detail)"
		if v.err != nil {
			msg = v.err.Error()
		}
		return v.th.StatusFail.Render("error: ") + msg
	}
	return ""
}
