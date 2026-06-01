// SPDX-License-Identifier: Apache-2.0

package theme

import "github.com/charmbracelet/lipgloss"

// HighContrast returns the accessibility-focused variant. Uses only
// the ANSI 16-colour base palette (foreground numbers 0–15) so the
// theme renders identically across every terminal kbark targets —
// no 256-colour or truecolor approximations to washed-out shades.
// Every status colour is fully saturated; every text colour is the
// terminal's brightest white. Selection is bright yellow on black
// for maximum reverse-video punch.
//
// Selected via profile.theme: high-contrast in ~/.config/kbark/config.yaml.
func HighContrast() Theme {
	const (
		fg       = "15" // bright white
		bg       = "0"  // black
		yellow   = "11"
		red      = "9"
		green    = "10"
		cyan     = "14"
		magenta  = "13"
		dimGrey  = "7" // light grey — still readable against bg=0
		darkGrey = "8" // dark grey — only for the disabled/dim case
	)

	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(fg)).
		Background(lipgloss.Color(bg)).
		Padding(0, 1)

	return Theme{
		Footer: footer,
		FooterAccent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(yellow)).
			Background(lipgloss.Color(bg)).
			Bold(true),
		FooterDim: lipgloss.NewStyle().
			Foreground(lipgloss.Color(dimGrey)).
			Background(lipgloss.Color(bg)),
		Content: lipgloss.NewStyle(),
		Accent: lipgloss.NewStyle().
			Foreground(lipgloss.Color(yellow)).
			Bold(true),

		StatusOK:   lipgloss.NewStyle().Foreground(lipgloss.Color(green)).Bold(true),
		StatusWarn: lipgloss.NewStyle().Foreground(lipgloss.Color(yellow)).Bold(true),
		StatusFail: lipgloss.NewStyle().Foreground(lipgloss.Color(red)).Bold(true),

		PhaseRunning:   lipgloss.NewStyle().Foreground(lipgloss.Color(green)).Bold(true),
		PhasePending:   lipgloss.NewStyle().Foreground(lipgloss.Color(yellow)).Bold(true),
		PhaseFailed:    lipgloss.NewStyle().Foreground(lipgloss.Color(red)).Bold(true),
		PhaseSucceeded: lipgloss.NewStyle().Foreground(lipgloss.Color(cyan)).Bold(true),
		PhaseUnknown:   lipgloss.NewStyle().Foreground(lipgloss.Color(darkGrey)),

		TableHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(fg)).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color(fg)),
		TableSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color(bg)).
			Background(lipgloss.Color(yellow)).
			Bold(true),
		TableCell: lipgloss.NewStyle().Padding(0, 1),
	}
}

// ResolveByName picks a theme by its canonical name. Empty / "default"
// returns the standard palette; "high-contrast" returns the
// accessibility variant. Unknown names fall back to default so a typo
// in profile.theme doesn't crash the program — the user can confirm
// the active theme via the visual difference.
func ResolveByName(name string) Theme {
	switch name {
	case "high-contrast", "hc":
		return HighContrast()
	default:
		return Default()
	}
}
