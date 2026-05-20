// SPDX-License-Identifier: Apache-2.0

// Package tui owns the bubbletea program: the top-level Model, the
// Update routing, and the View composition. Resource-specific views
// live under internal/tui/views/ (added in subsequent PRs).
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// Model is the root bubbletea model. Subsequent PRs add resource views
// inside the content area; for now the area is intentionally blank.
type Model struct {
	width, height int

	flags   *genericclioptions.ConfigFlags
	profile string
	mode    string

	contextName string
	namespace   string

	footer components.Footer
	keys   KeyMap
	th     theme.Theme
}

// NewModel constructs the root TUI model. It resolves the effective
// kubeconfig context and namespace eagerly so the footer can show them
// from frame one, even if no cluster traffic has happened yet.
func NewModel(flags *genericclioptions.ConfigFlags, profile string) Model {
	ctx, ns := resolveContextAndNamespace(flags)
	th := theme.Default()
	return Model{
		flags:       flags,
		profile:     profile,
		mode:        "RO",
		contextName: ctx,
		namespace:   ns,
		footer:      components.NewFooter(th),
		keys:        DefaultKeyMap(),
		th:          th,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if keyMatches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m, nil

	case NamespaceChangedMsg:
		m.namespace = msg.Namespace
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		// First frame before WindowSizeMsg arrives — render nothing.
		return ""
	}
	contentHeight := m.height - 1
	if contentHeight < 0 {
		contentHeight = 0
	}
	content := m.th.Content.Width(m.width).Height(contentHeight).Render("")
	foot := m.footer.View(m.width, components.FooterData{
		Context:   m.contextName,
		Namespace: m.namespace,
		Profile:   m.profile,
		Mode:      m.mode,
		Help:      "q quit · : cmd · ? AI",
	})
	return content + "\n" + foot
}

func resolveContextAndNamespace(flags *genericclioptions.ConfigFlags) (string, string) {
	ctx := "?"
	ns := "default"
	loader := flags.ToRawKubeConfigLoader()
	if raw, err := loader.RawConfig(); err == nil {
		if raw.CurrentContext != "" {
			ctx = raw.CurrentContext
		}
		if flags.Context != nil && *flags.Context != "" {
			ctx = *flags.Context
		}
	}
	if n, _, err := loader.Namespace(); err == nil && n != "" {
		ns = n
	}
	return ctx, ns
}
