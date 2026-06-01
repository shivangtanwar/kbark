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
// Owns a bubbles/viewport for scroll behaviour plus a per-line cursor
// (j/k or ↑/↓) so the user can target a specific line and ask the AI
// about it via `?`. Auto-follow is on by default and parks the cursor
// at the most recent line; navigating away from the bottom turns
// follow off (k9s/htop style).
type LogsView struct {
	vp     viewport.Model
	lines  []string
	cursor int // -1 when buffer is empty; otherwise index into lines
	follow bool
	title  string
	th     theme.Theme
}

func NewLogsView(th theme.Theme) LogsView {
	return LogsView{
		vp:     viewport.New(0, 0),
		cursor: -1,
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
	v.vp.SetContent(v.renderContent())
	v.ensureCursorVisible()
	return v
}

// AppendLines adds new lines to the buffer. When the buffer exceeds
// LogsViewMaxLines, the oldest are dropped (cursor adjusted to stay
// pointing at the same line, or clamped to 0). In follow mode the
// cursor snaps to the newest line.
func (v LogsView) AppendLines(lines []string) LogsView {
	if len(lines) == 0 {
		return v
	}
	preLen := len(v.lines)
	v.lines = append(v.lines, lines...)
	if len(v.lines) > LogsViewMaxLines {
		evicted := len(v.lines) - LogsViewMaxLines
		v.lines = v.lines[evicted:]
		v.cursor -= evicted
		if v.cursor < 0 {
			v.cursor = 0
		}
	}
	if v.follow || preLen == 0 {
		v.cursor = len(v.lines) - 1
	}
	v.vp.SetContent(v.renderContent())
	if v.follow {
		v.vp.GotoBottom()
	} else {
		v.ensureCursorVisible()
	}
	return v
}

// Reset clears the buffer. Used when the user switches to a different
// pod's logs without leaving the view.
func (v LogsView) Reset() LogsView {
	v.lines = nil
	v.cursor = -1
	v.vp.SetContent("")
	return v
}

// ToggleFollow flips the follow flag. When turning follow back on, snap
// the cursor and viewport back to the newest line so the next snapshot
// lands in view.
func (v LogsView) ToggleFollow() LogsView {
	v.follow = !v.follow
	if v.follow && len(v.lines) > 0 {
		v.cursor = len(v.lines) - 1
		v.vp.SetContent(v.renderContent())
		v.vp.GotoBottom()
	}
	return v
}

// Following reports the current follow state — used by the parent Model
// to render an indicator (paused/live) in the footer or title.
func (v LogsView) Following() bool { return v.follow }

// LineCount is exposed so the parent can render "N lines" indicator.
func (v LogsView) LineCount() int { return len(v.lines) }

// Cursor returns the current cursor position (-1 if the buffer is
// empty). Exposed for tests; the parent Model uses SelectedLine /
// LinesAround for the `?` flow.
func (v LogsView) Cursor() int { return v.cursor }

// SelectedLine returns the line at the cursor and its index. ok is
// false when the buffer is empty. The parent Model's `?` handler
// uses this as the focal point for the AI prompt.
func (v LogsView) SelectedLine() (line string, index int, ok bool) {
	if v.cursor < 0 || v.cursor >= len(v.lines) {
		return "", 0, false
	}
	return v.lines[v.cursor], v.cursor, true
}

// LinesAround returns the window of `before` lines before and `after`
// lines after the given index, inclusive of the index itself. Used to
// build the context payload around the cursor line. Edges of the
// buffer clamp naturally.
func (v LogsView) LinesAround(index, before, after int) []string {
	if index < 0 || index >= len(v.lines) {
		return nil
	}
	start := index - before
	if start < 0 {
		start = 0
	}
	end := index + after + 1
	if end > len(v.lines) {
		end = len(v.lines)
	}
	out := make([]string, end-start)
	copy(out, v.lines[start:end])
	return out
}

func (v LogsView) Update(msg tea.Msg) (LogsView, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		var cmd tea.Cmd
		v.vp, cmd = v.vp.Update(msg)
		return v, cmd
	}

	switch key.String() {
	case "j", "down":
		return v.moveCursor(1), nil
	case "k", "up":
		return v.moveCursor(-1), nil
	case "g":
		return v.jumpCursor(0), nil
	case "G":
		return v.jumpCursor(len(v.lines) - 1), nil
	}

	// Fall through to viewport for PageUp/PageDown/half-page bindings.
	// If the user lands off the bottom via page-scroll, also disable
	// follow — matches the prior behaviour.
	wasAtBottom := v.vp.AtBottom()
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	if wasAtBottom && !v.vp.AtBottom() {
		v.follow = false
	}
	return v, cmd
}

// moveCursor advances the cursor by `delta` lines (positive = down),
// clamps to buffer bounds, and adjusts follow + viewport scroll so
// the cursor stays visible.
func (v LogsView) moveCursor(delta int) LogsView {
	if len(v.lines) == 0 {
		return v
	}
	next := v.cursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(v.lines)-1 {
		next = len(v.lines) - 1
	}
	if next == v.cursor {
		return v
	}
	v.cursor = next
	// Moving up off the latest line turns follow off; moving back to
	// the bottom turns it back on.
	v.follow = v.cursor == len(v.lines)-1
	v.vp.SetContent(v.renderContent())
	v.ensureCursorVisible()
	return v
}

// jumpCursor moves the cursor to an absolute index (g/G handling).
func (v LogsView) jumpCursor(index int) LogsView {
	if len(v.lines) == 0 {
		return v
	}
	if index < 0 {
		index = 0
	}
	if index > len(v.lines)-1 {
		index = len(v.lines) - 1
	}
	v.cursor = index
	v.follow = v.cursor == len(v.lines)-1
	v.vp.SetContent(v.renderContent())
	v.ensureCursorVisible()
	return v
}

// ensureCursorVisible scrolls the viewport so the cursor line is on
// screen. No-op when the cursor is already in the visible window.
func (v *LogsView) ensureCursorVisible() {
	if v.cursor < 0 || v.vp.Height <= 0 {
		return
	}
	top := v.vp.YOffset
	bottom := top + v.vp.Height - 1
	if v.cursor < top {
		v.vp.SetYOffset(v.cursor)
	} else if v.cursor > bottom {
		v.vp.SetYOffset(v.cursor - v.vp.Height + 1)
	}
}

// renderContent rebuilds the full viewport string with the cursor
// line highlighted. Called on every cursor move and every append.
// O(N) per call; at LogsViewMaxLines (~1 MiB) that's negligible
// alongside the Charm render pipeline overhead.
func (v LogsView) renderContent() string {
	if len(v.lines) == 0 {
		return ""
	}
	if v.cursor < 0 || v.cursor >= len(v.lines) {
		return strings.Join(v.lines, "\n")
	}
	out := make([]string, len(v.lines))
	for i, line := range v.lines {
		if i == v.cursor {
			out[i] = v.th.TableSelected.Render(line)
		} else {
			out[i] = line
		}
	}
	return strings.Join(out, "\n")
}

func (v LogsView) View() string {
	body := v.vp.View()
	if v.title == "" {
		return body
	}
	return v.th.FooterAccent.Render(v.title) + "\n" + body
}
