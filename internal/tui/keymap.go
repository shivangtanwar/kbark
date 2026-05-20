// SPDX-License-Identifier: Apache-2.0

package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap is the global keybindings shared across every view.
// View-specific keys live alongside the view code.
type KeyMap struct {
	Quit     key.Binding
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Enter    key.Binding
	Search   key.Binding
	Command  key.Binding
	Diagnose key.Binding
	Help     key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:     key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "left")),
		Right:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "right")),
		Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Search:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Command:  key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "cmd")),
		Diagnose: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "AI")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "AI")),
	}
}
