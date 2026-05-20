// SPDX-License-Identifier: Apache-2.0

package components

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// Cmdbar is the `:`-activated command palette. v1 understands one
// command: `ns <namespace>`. The parent Model owns Activate/Deactivate
// and Value parsing; this component just renders and consumes keys.
type Cmdbar struct {
	input  textinput.Model
	active bool
	errMsg string
	th     theme.Theme
}

func NewCmdbar(th theme.Theme) Cmdbar {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.Placeholder = "ns <namespace>"
	ti.CharLimit = 80
	return Cmdbar{input: ti, th: th}
}

func (c Cmdbar) Active() bool  { return c.active }
func (c Cmdbar) Value() string { return c.input.Value() }

func (c Cmdbar) Activate() Cmdbar {
	c.active = true
	c.errMsg = ""
	c.input.SetValue("")
	c.input.Focus()
	return c
}

func (c Cmdbar) Deactivate() Cmdbar {
	c.active = false
	c.input.Blur()
	c.errMsg = ""
	return c
}

// SetError shows an inline error next to the input without closing the
// cmdbar — the user can correct their typing and try again.
func (c Cmdbar) SetError(msg string) Cmdbar {
	c.errMsg = msg
	return c
}

func (c Cmdbar) Update(msg tea.Msg) (Cmdbar, tea.Cmd) {
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

func (c Cmdbar) View(width int) string {
	if !c.active {
		return ""
	}
	line := c.input.View()
	if c.errMsg != "" {
		line = lipgloss.JoinHorizontal(lipgloss.Top, line, "  "+c.th.StatusFail.Render(c.errMsg))
	}
	return c.th.Footer.Width(width).Render(line)
}
