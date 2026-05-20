// SPDX-License-Identifier: Apache-2.0

// Package tui owns the bubbletea program: the top-level Model, the
// Update routing, and the View composition. Resource-specific views
// live under internal/tui/views/.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/ai"
	"github.com/shivangtanwar/kbark/internal/diagnose"
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
	ViewDiagnose
)

// ModelDeps bundles everything the root Model needs at construction time.
// Fields may be nil for tests that don't exercise the data path.
type ModelDeps struct {
	Ctx               context.Context
	Flags             *genericclioptions.ConfigFlags
	Profile           string
	PodService        *kube.PodService
	PodsCh            <-chan []*corev1.Pod
	PodsDone          <-chan struct{}
	LogService        *kube.LogService
	PodContextBuilder *diagnose.PodContextBuilder
	ToolDispatcher    *diagnose.Dispatcher
	AIProvider        ai.Provider
	AIModel           string
}

// Model is the root bubbletea model.
type Model struct {
	width, height int

	ctx context.Context

	flags   *genericclioptions.ConfigFlags
	profile string
	mode    string

	contextName string
	namespace   string

	active       ActiveView
	podView      views.PodView
	logsView     views.LogsView
	diagnoseView views.DiagnoseView
	cmdbar       components.Cmdbar

	podService     *kube.PodService
	logService     *kube.LogService
	podContextBldr *diagnose.PodContextBuilder
	toolDispatcher *diagnose.Dispatcher
	aiProvider     ai.Provider
	aiModel        string

	podsCh           <-chan []*corev1.Pod
	podsDone         <-chan struct{}
	logsCh           <-chan []string
	logsDone         <-chan struct{}
	diagnoseSession  *diagnose.Session
	diagnoseEventsCh <-chan ai.Event

	footer components.Footer
	keys   KeyMap
	th     theme.Theme
}

func NewModel(deps ModelDeps) Model {
	ctxName, ns := resolveContextAndNamespace(deps.Flags)
	th := theme.Default()
	parentCtx := deps.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	return Model{
		ctx:            parentCtx,
		flags:          deps.Flags,
		profile:        deps.Profile,
		mode:           "RO",
		contextName:    ctxName,
		namespace:      ns,
		active:         ViewPods,
		podView:        views.NewPodView(th),
		logsView:       views.NewLogsView(th),
		diagnoseView:   views.NewDiagnoseView(th),
		cmdbar:         components.NewCmdbar(th),
		podService:     deps.PodService,
		logService:     deps.LogService,
		podContextBldr: deps.PodContextBuilder,
		toolDispatcher: deps.ToolDispatcher,
		aiProvider:     deps.AIProvider,
		aiModel:        deps.AIModel,
		podsCh:         deps.PodsCh,
		podsDone:       deps.PodsDone,
		footer:         components.NewFooter(th),
		keys:           DefaultKeyMap(),
		th:             th,
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
		m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case PodsUpdatedMsg:
		m.podView = m.podView.SetPods(msg.Pods)
		return m, waitForPods(m.podsCh, m.podsDone)

	case NamespaceChangedMsg:
		return m.handleNamespaceChange(msg.Namespace)

	case LogsBatchMsg:
		m.logsView = m.logsView.AppendLines(msg.Lines)
		return m, waitForLogs(m.logsCh, m.logsDone)

	case LogsEndMsg:
		m.logsCh = nil
		m.logsDone = nil
		return m, nil

	case DiagnosisStartedMsg:
		m.diagnoseSession = msg.Session
		m.diagnoseEventsCh = msg.Session.Events()
		return m, waitForDiagnoseEvent(m.diagnoseEventsCh)

	case DiagnosisDeltaMsg:
		m.diagnoseView = m.diagnoseView.AppendText(msg.Text)
		return m, waitForDiagnoseEvent(m.diagnoseEventsCh)

	case DiagnosisDoneMsg:
		m.diagnoseView = m.diagnoseView.MarkDone()
		m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
		m.diagnoseEventsCh = nil
		return m, nil

	case DiagnosisErrorMsg:
		m.diagnoseView = m.diagnoseView.MarkError(msg.Err)
		m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
		m.diagnoseEventsCh = nil
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
			m.resizeAll()
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
		m.resizeAll()
		return m, nil
	}

	switch m.active {
	case ViewPods:
		switch msg.String() {
		case "l":
			return m.openLogs()
		case "?":
			return m.openDiagnose()
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

	case ViewDiagnose:
		if msg.String() == "esc" {
			return m.closeDiagnose()
		}
		var cmd tea.Cmd
		m.diagnoseView, cmd = m.diagnoseView.Update(msg)
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

func (m Model) openDiagnose() (Model, tea.Cmd) {
	pod := m.podView.SelectedPod()
	if pod == nil {
		return m, nil
	}
	m.active = ViewDiagnose
	m.diagnoseView = m.diagnoseView.Reset()
	m.diagnoseView = m.diagnoseView.SetTitle(fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())

	if m.aiProvider == nil || m.podContextBldr == nil {
		err := errors.New("AI not configured (set ANTHROPIC_API_KEY and restart)")
		m.diagnoseView = m.diagnoseView.MarkError(err)
		m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
		return m, nil
	}

	return m, startDiagnosis(m.ctx, m.podContextBldr, m.toolDispatcher, m.aiProvider, m.aiModel, pod)
}

func (m Model) closeDiagnose() (Model, tea.Cmd) {
	if m.diagnoseSession != nil {
		m.diagnoseSession.Cancel()
		m.diagnoseSession = nil
	}
	m.diagnoseEventsCh = nil
	m.active = ViewPods
	return m, nil
}

func (m *Model) resizeAll() {
	m.podView = m.podView.SetSize(m.width, m.contentHeight())
	m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
	m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
}

func (m Model) submitCmd() (Model, tea.Cmd) {
	input := strings.TrimSpace(m.cmdbar.Value())
	parts := strings.Fields(input)
	if len(parts) == 2 && parts[0] == "ns" {
		m.cmdbar = m.cmdbar.Deactivate()
		m.resizeAll()
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
	// Namespace switch always returns to the pod view; any in-flight
	// diagnose or logs stream for a now-foreign pod becomes meaningless.
	if m.active != ViewPods {
		if m.logService != nil {
			m.logService.Stop()
		}
		if m.diagnoseSession != nil {
			m.diagnoseSession.Cancel()
			m.diagnoseSession = nil
		}
		m.active = ViewPods
		m.logsCh = nil
		m.logsDone = nil
		m.diagnoseEventsCh = nil
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
	case ViewDiagnose:
		content = m.diagnoseView.View()
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
	case ViewDiagnose:
		return "esc dismiss · q quit"
	default:
		return "l logs · ? AI · q quit · : cmd"
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

// startDiagnosis builds the pod context payload (cheap API calls under a
// 3s log-read budget) and opens a streaming session. Returned as a Cmd so
// the UI isn't blocked while context assembly is in flight. The session
// runs the tool-call loop internally; dispatcher may be nil for
// providers that don't support tools (Ollama falls back to one-shot).
func startDiagnosis(
	ctx context.Context,
	builder *diagnose.PodContextBuilder,
	dispatcher *diagnose.Dispatcher,
	provider ai.Provider,
	model string,
	pod *corev1.Pod,
) tea.Cmd {
	return func() tea.Msg {
		payload := builder.Build(ctx, pod)
		session := diagnose.Start(ctx, provider, model, payload, dispatcher)
		return DiagnosisStartedMsg{Session: session, Pod: pod}
	}
}

// waitForDiagnoseEvent blocks on the session's events channel and
// translates the next ai.Event into the corresponding bubbletea message.
// Returns nil when the channel closes without a Done/Error event so the
// Cmd loop quietly exits.
func waitForDiagnoseEvent(ch <-chan ai.Event) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		ev, ok := <-ch
		if !ok {
			return DiagnosisDoneMsg{StopReason: "closed"}
		}
		switch e := ev.(type) {
		case ai.TextDeltaEvent:
			return DiagnosisDeltaMsg{Text: e.Delta}
		case ai.DoneEvent:
			return DiagnosisDoneMsg{StopReason: e.StopReason}
		case ai.ErrorEvent:
			return DiagnosisErrorMsg{Err: e.Err}
		case ai.ToolCallEvent:
			// M6 handles these. For now, ignore so the stream keeps flowing.
			return nil
		}
		return nil
	}
}

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
