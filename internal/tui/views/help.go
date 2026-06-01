// SPDX-License-Identifier: Apache-2.0

package views

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// HelpView is the cheat-sheet modal opened by `:help` in the cmdbar.
// Contents are passed in by the parent Model (so it can interpolate
// runtime state — active profile, configured profiles, cache dir)
// rather than hard-coded here.
type HelpView struct {
	vp      viewport.Model
	content string
	th      theme.Theme
}

func NewHelpView(th theme.Theme) HelpView {
	return HelpView{
		vp: viewport.New(0, 0),
		th: th,
	}
}

// SetContent replaces the rendered help text.
func (v HelpView) SetContent(s string) HelpView {
	v.content = s
	v.vp.SetContent(wrapBufferToWidth(s, v.vp.Width))
	v.vp.GotoTop()
	return v
}

func (v HelpView) SetSize(width, height int) HelpView {
	v.vp.Width = width
	// One status line at the bottom.
	inner := height - 1
	if inner < 0 {
		inner = 0
	}
	v.vp.Height = inner
	v.vp.SetContent(wrapBufferToWidth(v.content, v.vp.Width))
	return v
}

func (v HelpView) Update(msg tea.Msg) (HelpView, tea.Cmd) {
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v HelpView) View() string {
	body := v.vp.View()
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")).
		Render("— scroll with j/k or PgUp/PgDn · esc dismisses —")
	return body + "\n" + hint
}
