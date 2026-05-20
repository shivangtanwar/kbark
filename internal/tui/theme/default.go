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

	// Abstract doctor-style status colors (used by `kbark doctor`).
	StatusOK   lipgloss.Style
	StatusWarn lipgloss.Style
	StatusFail lipgloss.Style

	// Concrete pod-phase colors (used by the pod table view).
	PhaseRunning   lipgloss.Style
	PhasePending   lipgloss.Style
	PhaseFailed    lipgloss.Style
	PhaseSucceeded lipgloss.Style
	PhaseUnknown   lipgloss.Style

	// Table styles.
	TableHeader   lipgloss.Style
	TableSelected lipgloss.Style
	TableCell     lipgloss.Style
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

		// Dim green to keep "Running" calm — the common case shouldn't shout.
		PhaseRunning:   lipgloss.NewStyle().Foreground(lipgloss.Color("34")),
		PhasePending:   lipgloss.NewStyle().Foreground(lipgloss.Color("220")),
		PhaseFailed:    lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		PhaseSucceeded: lipgloss.NewStyle().Foreground(lipgloss.Color("24")),
		PhaseUnknown:   lipgloss.NewStyle().Foreground(lipgloss.Color("245")),

		TableHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("237")),
		TableSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("232")).
			Background(lipgloss.Color("117")).
			Bold(true),
		TableCell: lipgloss.NewStyle().
			Padding(0, 1),
	}
}

// Phase returns the lipgloss style appropriate for a Kubernetes pod phase.
// Anything not in {Running, Pending, Failed, Succeeded} renders as Unknown.
func (t Theme) Phase(phase string) lipgloss.Style {
	switch phase {
	case "Running":
		return t.PhaseRunning
	case "Pending":
		return t.PhasePending
	case "Failed":
		return t.PhaseFailed
	case "Succeeded":
		return t.PhaseSucceeded
	default:
		return t.PhaseUnknown
	}
}
