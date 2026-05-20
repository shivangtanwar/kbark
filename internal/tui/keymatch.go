// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMatches is a tiny wrapper that lets the Model check a tea.KeyMsg
// against a configured key.Binding. Pulled out so each Update branch
// reads as `if keyMatches(msg, m.keys.Quit) {…}`.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
}
