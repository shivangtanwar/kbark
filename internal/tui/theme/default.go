// SPDX-License-Identifier: Apache-2.0

// Package theme owns the lipgloss styles used across the TUI.
// Default() returns the canonical kbark palette; a high-contrast
// variant lands later, gated behind a runtime flag.
package theme

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	// Footer styles the persistent bottom bar. Inverted-ish.
	Footer lipgloss.Style
	// FooterAccent highlights individual fields inside the footer.
	FooterAccent lipgloss.Style
	// FooterDim is used for the key-help text on the right side.
	FooterDim lipgloss.Style
	// Content is the empty placeholder style for the main area.
	Content lipgloss.Style
	// Accent — single brand accent used for selection and emphasis.
	Accent lipgloss.Style
	// Status colors for resource rows (populated in PR #7).
	StatusOK   lipgloss.Style
	StatusWarn lipgloss.Style
	StatusFail lipgloss.Style
}

func Default() Theme {
	return Theme{
		Footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Background(lipgloss.Color("237")).
			Padding(0, 1),
		FooterAccent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("117")).
			Background(lipgloss.Color("237")).
			Bold(true),
		FooterDim: lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Background(lipgloss.Color("237")),
		Content: lipgloss.NewStyle(),
		Accent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")),
		StatusOK:   lipgloss.NewStyle().Foreground(lipgloss.Color("46")),
		StatusWarn: lipgloss.NewStyle().Foreground(lipgloss.Color("220")),
		StatusFail: lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	}
}
