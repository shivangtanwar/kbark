// SPDX-License-Identifier: Apache-2.0

// Package tui owns the bubbletea program: the top-level Model, the
// Update routing, and the View composition. Resource-specific views
// live under internal/tui/views/.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

// Model is the root bubbletea model. It embeds whichever resource view
// is currently active (only PodView in M1) and renders the persistent
// footer beneath it.
type Model struct {
	width, height int

	flags   *genericclioptions.ConfigFlags
	profile string
	mode    string

	contextName string
	namespace   string

	podView   views.PodView
	snapshots <-chan []*corev1.Pod

	footer components.Footer
	keys   KeyMap
	th     theme.Theme
}

// NewModel constructs the root TUI model. `snapshots` is the channel the
// kube package's PodLister writes to; pass nil during tests that don't
// exercise the data path.
func NewModel(flags *genericclioptions.ConfigFlags, profile string, snapshots <-chan []*corev1.Pod) Model {
	ctx, ns := resolveContextAndNamespace(flags)
	th := theme.Default()
	return Model{
		flags:       flags,
		profile:     profile,
		mode:        "RO",
		contextName: ctx,
		namespace:   ns,
		podView:     views.NewPodView(th),
		snapshots:   snapshots,
		footer:      components.NewFooter(th),
		keys:        DefaultKeyMap(),
		th:          th,
	}
}

func (m Model) Init() tea.Cmd {
	if m.snapshots == nil {
		return nil
	}
	return waitForPods(m.snapshots)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		return m, nil

	case tea.KeyMsg:
		if keyMatches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		// Forward navigation keys to the pod view (table scrolling).
		var cmd tea.Cmd
		m.podView, cmd = m.podView.Update(msg)
		return m, cmd

	case PodsUpdatedMsg:
		m.podView = m.podView.SetPods(msg.Pods)
		// Keep listening for the next snapshot.
		return m, waitForPods(m.snapshots)

	case NamespaceChangedMsg:
		m.namespace = msg.Namespace
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	content := m.podView.View()
	foot := m.footer.View(m.width, components.FooterData{
		Context:   m.contextName,
		Namespace: m.namespace,
		Profile:   m.profile,
		Mode:      m.mode,
		Help:      "q quit · : cmd · ? AI",
	})
	return content + "\n" + foot
}

func (m Model) contentHeight() int {
	h := m.height - 1
	if h < 0 {
		return 0
	}
	return h
}

// waitForPods blocks on the snapshot channel and converts each receive
// into a PodsUpdatedMsg. After handling, Update returns this Cmd again
// to keep the bridge alive for the next snapshot.
func waitForPods(ch <-chan []*corev1.Pod) tea.Cmd {
	return func() tea.Msg {
		pods, ok := <-ch
		if !ok {
			return nil
		}
		return PodsUpdatedMsg{Pods: pods}
	}
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
