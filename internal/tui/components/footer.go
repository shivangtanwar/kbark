// SPDX-License-Identifier: Apache-2.0

// Package components holds reusable TUI building blocks (footer, table,
// cmdbar, modal). Each component is a small struct with a View method;
// they don't own their own bubbletea state, the parent Model does.
package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// FooterData is what the parent Model passes in on every render.
type FooterData struct {
	Context   string
	Namespace string
	Profile   string
	Mode      string
	Help      string
}

type Footer struct {
	th theme.Theme
}

func NewFooter(th theme.Theme) Footer {
	return Footer{th: th}
}

// View renders the footer at exactly `width` columns. Long contexts get
// truncated so the help text on the right stays visible.
func (f Footer) View(width int, d FooterData) string {
	left := fmt.Sprintf("ctx: %s · ns: %s · profile: %s · mode: %s",
		d.Context, d.Namespace, d.Profile, d.Mode)
	right := d.Help

	// Account for the footer's horizontal padding (1 each side, applied by
	// the style below).
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	switch {
	case leftW+rightW+1 <= innerWidth:
		gap := strings.Repeat(" ", innerWidth-leftW-rightW)
		return f.th.Footer.Width(width).Render(left + gap + right)
	case rightW < innerWidth:
		// Truncate the left so the help text survives.
		budget := innerWidth - rightW - 2
		if budget < 1 {
			budget = 1
		}
		truncated := truncate(left, budget) + "  "
		return f.th.Footer.Width(width).Render(truncated + right)
	default:
		// Window too narrow even for help; just show whatever fits of the left.
		return f.th.Footer.Width(width).Render(truncate(left, innerWidth))
	}
}

func truncate(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	// Drop trailing runes until we fit, leaving room for the ellipsis.
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > max {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
