// SPDX-License-Identifier: Apache-2.0

// Package tui owns the bubbletea program: the top-level Model, the
// Update routing, and the View composition. Resource-specific views
// live under internal/tui/views/.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

// ActiveView is the currently-routed-to resource view. Resource-specific
// views are mutually exclusive in the content area; only one is rendered
// and receives forwarded key events at a time.
type ActiveView int

const (
	ViewPods ActiveView = iota
	ViewLogs
)

// Model is the root bubbletea model. It owns every resource view, the
// command bar, the persistent footer, and the kube services that manage
// informer / log-streamer lifecycles.
type Model struct {
	width, height int

	flags   *genericclioptions.ConfigFlags
	profile string
	mode    string

	contextName string
	namespace   string

	active     ActiveView
	podView    views.PodView
	logsView   views.LogsView
	cmdbar     components.Cmdbar
	podService *kube.PodService
	logService *kube.LogService

	podsCh   <-chan []*corev1.Pod
	podsDone <-chan struct{}
	logsCh   <-chan []string
	logsDone <-chan struct{}

	footer components.Footer
	keys   KeyMap
	th     theme.Theme
}

// NewModel constructs the root model. Services and channels may be nil
// for tests that don't exercise the data path.
func NewModel(
	flags *genericclioptions.ConfigFlags,
	profile string,
	podService *kube.PodService,
	podsCh <-chan []*corev1.Pod,
	podsDone <-chan struct{},
	logService *kube.LogService,
) Model {
	ctx, ns := resolveContextAndNamespace(flags)
	th := theme.Default()
	return Model{
		flags:       flags,
		profile:     profile,
		mode:        "RO",
		contextName: ctx,
		namespace:   ns,
		active:      ViewPods,
		podView:     views.NewPodView(th),
		logsView:    views.NewLogsView(th),
		cmdbar:      components.NewCmdbar(th),
		podService:  podService,
		logService:  logService,
		podsCh:      podsCh,
		podsDone:    podsDone,
		footer:      components.NewFooter(th),
		keys:        DefaultKeyMap(),
		th:          th,
	}
}

func (m Model) Init() tea.Cmd {
	if m.podsCh == nil {
		return nil
	}
	return waitForPods(m.podsCh, m.podsDone)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case PodsUpdatedMsg:
		// Pod state updates regardless of active view — when the user
		// switches back from logs, the table is fresh.
		m.podView = m.podView.SetPods(msg.Pods)
		return m, waitForPods(m.podsCh, m.podsDone)

	case NamespaceChangedMsg:
		return m.handleNamespaceChange(msg.Namespace)

	case LogsBatchMsg:
		m.logsView = m.logsView.AppendLines(msg.Lines)
		return m, waitForLogs(m.logsCh, m.logsDone)

	case LogsEndMsg:
		// Stream ended naturally; stop re-arming waitForLogs but stay in
		// the view so the user can read what was buffered.
		m.logsCh = nil
		m.logsDone = nil
		return m, nil
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
			m.podView = m.podView.SetSize(m.width, m.contentHeight())
			m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
			return m, nil
		default:
			var cmd tea.Cmd
			m.cmdbar, cmd = m.cmdbar.Update(msg)
			return m, cmd
		}
	}

	// Global keys regardless of active view.
	if keyMatches(msg, m.keys.Quit) {
		return m, tea.Quit
	}
	if keyMatches(msg, m.keys.Command) {
		m.cmdbar = m.cmdbar.Activate()
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
		return m, nil
	}

	// View-specific.
	switch m.active {
	case ViewPods:
		if msg.String() == "l" {
			return m.openLogs()
		}
		var cmd tea.Cmd
		m.podView, cmd = m.podView.Update(msg)
		return m, cmd

	case ViewLogs:
		switch msg.String() {
		case "esc":
			return m.closeLogs()
		case "f":
			m.logsView = m.logsView.ToggleFollow()
			return m, nil
		}
		var cmd tea.Cmd
		m.logsView, cmd = m.logsView.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) openLogs() (Model, tea.Cmd) {
	pod := m.podView.SelectedPod()
	if pod == nil || m.logService == nil {
		return m, nil
	}
	streamer, err := m.logService.Stream(pod.Namespace, pod.Name, "", kube.LogOptions{
		Follow:    true,
		TailLines: 200,
	})
	if err != nil {
		m.cmdbar = m.cmdbar.Activate().SetError("logs: " + err.Error())
		return m, nil
	}
	m.active = ViewLogs
	m.logsView = m.logsView.Reset()
	m.logsView = m.logsView.SetTitle(fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
	m.logsCh = streamer.Snapshots()
	m.logsDone = streamer.Done()
	return m, waitForLogs(m.logsCh, m.logsDone)
}

func (m Model) closeLogs() (Model, tea.Cmd) {
	if m.logService != nil {
		m.logService.Stop()
	}
	m.active = ViewPods
	m.logsCh = nil
	m.logsDone = nil
	return m, nil
}

func (m Model) submitCmd() (Model, tea.Cmd) {
	input := strings.TrimSpace(m.cmdbar.Value())
	parts := strings.Fields(input)
	if len(parts) == 2 && parts[0] == "ns" {
		m.cmdbar = m.cmdbar.Deactivate()
		m.podView = m.podView.SetSize(m.width, m.contentHeight())
		m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
		ns := parts[1]
		return m, func() tea.Msg { return NamespaceChangedMsg{Namespace: ns} }
	}
	m.cmdbar = m.cmdbar.SetError("unknown: " + input)
	return m, nil
}

func (m Model) handleNamespaceChange(namespace string) (Model, tea.Cmd) {
	if m.podService == nil {
		m.namespace = namespace
		return m, nil
	}
	ch, done, err := m.podService.Switch(namespace)
	if err != nil {
		m.cmdbar = m.cmdbar.Activate().SetError("switch failed: " + err.Error())
		return m, nil
	}
	m.namespace = namespace
	m.podsCh = ch
	m.podsDone = done
	m.podView = m.podView.SetPods(nil)
	// Namespace switch always returns to the pod view; an open logs stream
	// for a now-foreign pod becomes meaningless.
	if m.active == ViewLogs {
		if m.logService != nil {
			m.logService.Stop()
		}
		m.active = ViewPods
		m.logsCh = nil
		m.logsDone = nil
	}
	return m, waitForPods(ch, done)
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	var content string
	switch m.active {
	case ViewLogs:
		content = m.logsView.View()
	default:
		content = m.podView.View()
	}
	parts := []string{content}
	if m.cmdbar.Active() {
		parts = append(parts, m.cmdbar.View(m.width))
	}
	parts = append(parts, m.footer.View(m.width, components.FooterData{
		Context:   m.contextName,
		Namespace: m.namespace,
		Profile:   m.profile,
		Mode:      m.mode,
		Help:      m.helpForView(),
	}))
	return strings.Join(parts, "\n")
}

func (m Model) helpForView() string {
	switch m.active {
	case ViewLogs:
		followKey := "f pause"
		if !m.logsView.Following() {
			followKey = "f follow"
		}
		return "esc back · " + followKey + " · q quit · ? AI"
	default:
		return "l logs · q quit · : cmd · ? AI"
	}
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

// waitForLogs is the same pattern for the log streamer. Returns LogsEndMsg
// when the streamer's done channel closes so the Model can stop re-arming.
func waitForLogs(ch <-chan []string, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		select {
		case lines, ok := <-ch:
			if !ok {
				return LogsEndMsg{}
			}
			return LogsBatchMsg{Lines: lines}
		case <-done:
			return LogsEndMsg{}
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
