// SPDX-License-Identifier: Apache-2.0

// Package tui owns the bubbletea program: the top-level Model, the
// Update routing, and the View composition. Resource-specific views
// live under internal/tui/views/.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

// Model is the root bubbletea model. It owns the resource view, the
// command bar, the persistent footer, and the PodService that switches
// namespace-scoped listers under the hood.
type Model struct {
	width, height int

	flags   *genericclioptions.ConfigFlags
	profile string
	mode    string

	contextName string
	namespace   string

	podView   views.PodView
	cmdbar    components.Cmdbar
	service   *kube.PodService
	snapshots <-chan []*corev1.Pod
	done      <-chan struct{}

	footer components.Footer
	keys   KeyMap
	th     theme.Theme
}

// NewModel constructs the root model. Pass nil `service` (and nil
// snapshots/done) for tests that don't exercise the data path.
func NewModel(
	flags *genericclioptions.ConfigFlags,
	profile string,
	service *kube.PodService,
	snapshots <-chan []*corev1.Pod,
	done <-chan struct{},
) Model {
	ctx, ns := resolveContextAndNamespace(flags)
	th := theme.Default()
	return Model{
		flags:       flags,
		profile:     profile,
		mode:        "RO",
		contextName: ctx,
		namespace:   ns,
		podView:     views.NewPodView(th),
		cmdbar:      components.NewCmdbar(th),
		service:     service,
		snapshots:   snapshots,
		done:        done,
		footer:      components.NewFooter(th),
		keys:        DefaultKeyMap(),
		th:          th,
	}
}

func (m Model) Init() tea.Cmd {
	if m.snapshots == nil {
		return nil
	}
	return waitForPods(m.snapshots, m.done)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case PodsUpdatedMsg:
		m.podView = m.podView.SetPods(msg.Pods)
		return m, waitForPods(m.snapshots, m.done)

	case NamespaceChangedMsg:
		return m.handleNamespaceChange(msg.Namespace)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cmdbar.Active() {
		switch msg.String() {
		case "enter":
			return m.submitCmd()
		case "esc":
			m.cmdbar = m.cmdbar.Deactivate()
			return m, nil
		default:
			var cmd tea.Cmd
			m.cmdbar, cmd = m.cmdbar.Update(msg)
			return m, cmd
		}
	}
	if keyMatches(msg, m.keys.Quit) {
		return m, tea.Quit
	}
	if keyMatches(msg, m.keys.Command) {
		m.cmdbar = m.cmdbar.Activate()
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		return m, nil
	}
	var cmd tea.Cmd
	m.podView, cmd = m.podView.Update(msg)
	return m, cmd
}

func (m Model) submitCmd() (Model, tea.Cmd) {
	input := strings.TrimSpace(m.cmdbar.Value())
	parts := strings.Fields(input)
	if len(parts) == 2 && parts[0] == "ns" {
		m.cmdbar = m.cmdbar.Deactivate()
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		ns := parts[1]
		return m, func() tea.Msg { return NamespaceChangedMsg{Namespace: ns} }
	}
	m.cmdbar = m.cmdbar.SetError("unknown: " + input)
	return m, nil
}

func (m Model) handleNamespaceChange(namespace string) (Model, tea.Cmd) {
	if m.service == nil {
		// Tests / headless mode: just record the rename, no lister churn.
		m.namespace = namespace
		return m, nil
	}
	ch, done, err := m.service.Switch(namespace)
	if err != nil {
		m.cmdbar = m.cmdbar.Activate().SetError("switch failed: " + err.Error())
		return m, nil
	}
	m.namespace = namespace
	m.snapshots = ch
	m.done = done
	m.podView = m.podView.SetPods(nil)
	return m, waitForPods(ch, done)
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	parts := []string{m.podView.View()}
	if m.cmdbar.Active() {
		parts = append(parts, m.cmdbar.View(m.width))
	}
	parts = append(parts, m.footer.View(m.width, components.FooterData{
		Context:   m.contextName,
		Namespace: m.namespace,
		Profile:   m.profile,
		Mode:      m.mode,
		Help:      "q quit · : cmd · ? AI",
	}))
	return strings.Join(parts, "\n")
}

func (m Model) contentHeight() int {
	h := m.height - 1 // footer
	if m.cmdbar.Active() {
		h-- // cmdbar above footer
	}
	if h < 0 {
		return 0
	}
	return h
}

// waitForPods blocks on the snapshot channel until either a snapshot
// arrives (PodsUpdatedMsg) or the done channel closes (no-op return).
// The done branch is what lets old waitForPods Cmds exit cleanly when
// PodService.Switch tears down the previous namespace's lister.
func waitForPods(ch <-chan []*corev1.Pod, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case pods, ok := <-ch:
			if !ok {
				return nil
			}
			return PodsUpdatedMsg{Pods: pods}
		case <-done:
			return nil
		}
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
