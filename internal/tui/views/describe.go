// SPDX-License-Identifier: Apache-2.0

package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// DescribeMode toggles which buffer the modal renders.
type DescribeMode int

const (
	// ModeDescribe shows the kubectl-style human-readable describe
	// output (events inline, kind-specific layout). Default.
	ModeDescribe DescribeMode = iota
	// ModeYAML shows the raw YAML serialisation of the cached object.
	// Available instantly; useful while describe is still loading.
	ModeYAML
)

// DescribeView renders the Enter-key modal: a scrollable pane that
// toggles between describe text and YAML via `y`. Both buffers can be
// set independently — YAML lands instantly off the cached object,
// describe streams in once kubectl/describe returns.
type DescribeView struct {
	vp           viewport.Model
	title        string
	describe     string
	yaml         string
	mode         DescribeMode
	describeWait bool
	err          error
	th           theme.Theme
}

func NewDescribeView(th theme.Theme) DescribeView {
	return DescribeView{
		vp:           viewport.New(0, 0),
		mode:         ModeDescribe,
		describeWait: true,
		th:           th,
	}
}

// Reset clears prior state — called when the modal is reopened on a
// different selection.
func (v DescribeView) Reset() DescribeView {
	v.title = ""
	v.describe = ""
	v.yaml = ""
	v.mode = ModeDescribe
	v.describeWait = true
	v.err = nil
	v.vp.SetContent("")
	return v
}

// SetTitle sets the modal's header ("<namespace>/<name> · <Kind>").
func (v DescribeView) SetTitle(title string) DescribeView {
	v.title = title
	return v
}

func (v DescribeView) SetSize(width, height int) DescribeView {
	v.vp.Width = width
	titleH, statusH := 0, 0
	if v.title != "" {
		titleH = 1
	}
	statusH = 1 // always show mode/help line
	inner := height - titleH - statusH
	if inner < 0 {
		inner = 0
	}
	v.vp.Height = inner
	v.vp.SetContent(wrapBufferToWidth(v.activeBuffer(), v.vp.Width))
	return v
}

// wrapBufferToWidth soft-wraps the modal buffer to fit the viewport
// width. Same approach as DiagnoseView — viewport doesn't word-wrap
// by default, so long describe lines (e.g. Image ID with a SHA) would
// otherwise run off the right edge. Width<=0 returns unchanged so the
// pre-SetSize render doesn't munge the buffer.
func wrapBufferToWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

// SetYAML stashes the YAML buffer. Called synchronously when the
// modal opens, so YAML is available immediately even while describe
// is loading.
func (v DescribeView) SetYAML(y string) DescribeView {
	v.yaml = y
	v.vp.SetContent(wrapBufferToWidth(v.activeBuffer(), v.vp.Width))
	return v
}

// SetDescribe stashes the describe buffer and clears the
// "describe pending" state.
func (v DescribeView) SetDescribe(d string) DescribeView {
	v.describe = d
	v.describeWait = false
	v.vp.SetContent(wrapBufferToWidth(v.activeBuffer(), v.vp.Width))
	v.vp.GotoTop()
	return v
}

// MarkError records a describe-fetch failure. The modal stays open;
// the YAML view (if available) keeps working.
func (v DescribeView) MarkError(err error) DescribeView {
	v.err = err
	v.describeWait = false
	v.vp.SetContent(wrapBufferToWidth(v.activeBuffer(), v.vp.Width))
	return v
}

// ToggleMode flips between describe and YAML. `y` keystroke handler.
func (v DescribeView) ToggleMode() DescribeView {
	if v.mode == ModeDescribe {
		v.mode = ModeYAML
	} else {
		v.mode = ModeDescribe
	}
	v.vp.SetContent(wrapBufferToWidth(v.activeBuffer(), v.vp.Width))
	v.vp.GotoTop()
	return v
}

// Mode exposes the current mode for tests and footer rendering.
func (v DescribeView) Mode() DescribeMode { return v.mode }

func (v DescribeView) Update(msg tea.Msg) (DescribeView, tea.Cmd) {
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v DescribeView) View() string {
	parts := make([]string, 0, 3)
	if v.title != "" {
		parts = append(parts, v.th.FooterAccent.Render(v.title))
	}
	parts = append(parts, v.vp.View())
	parts = append(parts, v.renderStatus())
	return strings.Join(parts, "\n")
}

func (v DescribeView) activeBuffer() string {
	switch v.mode {
	case ModeYAML:
		if v.yaml == "" {
			return "(yaml unavailable)"
		}
		return v.yaml
	default:
		if v.err != nil {
			// Describe failed but YAML may still be useful; tell the
			// user to switch with 'y' rather than leaving them stuck.
			return "describe failed: " + v.err.Error() + "\n\npress 'y' for YAML"
		}
		if v.describeWait {
			return "loading describe…"
		}
		return v.describe
	}
}

func (v DescribeView) renderStatus() string {
	mode := "describe"
	if v.mode == ModeYAML {
		mode = "yaml"
	}
	hint := "y toggle yaml/describe · esc back"
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	return dim.Render("— " + mode + " · " + hint + " —")
}
