// SPDX-License-Identifier: Apache-2.0

package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// LogsViewMaxLines caps the in-view buffer. Above this, the oldest lines
// are evicted FIFO. 10k lines is roughly 1 MiB of text — enough to scroll
// through a crash trace, small enough to not pressure the rendering loop.
const LogsViewMaxLines = 10000

// LogsView renders streaming log lines for a single pod/container.
// It owns a bubbles/viewport for scroll behaviour; the parent Model
// is responsible for feeding it lines via AppendLines and for handling
// Esc to close the view.
type LogsView struct {
	vp     viewport.Model
	lines  []string
	follow bool
	title  string
	th     theme.Theme
}

func NewLogsView(th theme.Theme) LogsView {
	return LogsView{
		vp:     viewport.New(0, 0),
		follow: true,
		th:     th,
	}
}

// SetTitle sets the one-line header rendered above the log content
// (typically "namespace/pod • container").
func (v LogsView) SetTitle(title string) LogsView {
	v.title = title
	return v
}

func (v LogsView) SetSize(width, height int) LogsView {
	v.vp.Width = width
	titleHeight := 0
	if v.title != "" {
		titleHeight = 1
	}
	innerHeight := height - titleHeight
	if innerHeight < 0 {
		innerHeight = 0
	}
	v.vp.Height = innerHeight
	return v
}

// AppendLines adds new lines to the buffer. When the buffer exceeds
// LogsViewMaxLines, the oldest are dropped. In follow mode the viewport
// scrolls to bottom after each append.
func (v LogsView) AppendLines(lines []string) LogsView {
	v.lines = append(v.lines, lines...)
	if len(v.lines) > LogsViewMaxLines {
		v.lines = v.lines[len(v.lines)-LogsViewMaxLines:]
	}
	v.vp.SetContent(strings.Join(v.lines, "\n"))
	if v.follow {
		v.vp.GotoBottom()
	}
	return v
}

// Reset clears the buffer. Used when the user switches to a different
// pod's logs without leaving the view.
func (v LogsView) Reset() LogsView {
	v.lines = nil
	v.vp.SetContent("")
	return v
}

// ToggleFollow flips the follow flag. When turning follow back on, jump
// to the bottom so the next snapshot lands in view.
func (v LogsView) ToggleFollow() LogsView {
	v.follow = !v.follow
	if v.follow {
		v.vp.GotoBottom()
	}
	return v
}

// Following reports the current follow state — used by the parent Model
// to render an indicator (paused/live) in the footer or title.
func (v LogsView) Following() bool { return v.follow }

// LineCount is exposed so the parent can render "N lines" indicator.
func (v LogsView) LineCount() int { return len(v.lines) }

func (v LogsView) Update(msg tea.Msg) (LogsView, tea.Cmd) {
	var cmd tea.Cmd
	// Detect a manual scroll-up: if the user moves off the bottom, they've
	// signalled they want to pause auto-scroll. Disable follow accordingly.
	if _, isKey := msg.(tea.KeyMsg); isKey {
		wasAtBottom := v.vp.AtBottom()
		v.vp, cmd = v.vp.Update(msg)
		if wasAtBottom && !v.vp.AtBottom() {
			v.follow = false
		}
		return v, cmd
	}
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v LogsView) View() string {
	body := v.vp.View()
	if v.title == "" {
		return body
	}
	return v.th.FooterAccent.Render(v.title) + "\n" + body
}
