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
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/ai"
	"github.com/shivangtanwar/kbark/internal/describe"
	"github.com/shivangtanwar/kbark/internal/diagnose"
	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
	"github.com/shivangtanwar/kbark/internal/tui/views"
)

// ActiveView is the currently-routed-to resource view. Resource-specific
// views are mutually exclusive in the content area; only one is rendered
// and receives forwarded key events at a time.
type ActiveView int

const (
	// ViewResource is the home: a TableResourceView for whatever kind
	// the user is currently looking at. Pods (kind="po") is the
	// default landing; `:dep`/`:svc`/etc. swap in the corresponding
	// kind. Pod-specific keys (l, ?) are gated on resourceKind=="po".
	ViewResource ActiveView = iota
	ViewLogs
	ViewDiagnose
	// ViewDescribe is the Enter-key modal: kubectl-style describe text
	// + a `y`-toggle to raw YAML. Stacks over the resource view so
	// esc returns there.
	ViewDescribe
)

// ModelDeps bundles everything the root Model needs at construction time.
// Fields may be nil for tests that don't exercise the data path.
type ModelDeps struct {
	Ctx               context.Context
	Flags             *genericclioptions.ConfigFlags
	Profile           string
	LogService        *kube.LogService
	PodContextBuilder *diagnose.PodContextBuilder
	LogContextBuilder *diagnose.LogContextBuilder
	ToolDispatcher    *diagnose.Dispatcher
	AIProvider        ai.Provider
	AIModel           string
	// DescribeService powers the Enter-key modal. May be nil if no
	// REST config could be built at startup; the modal then surfaces
	// YAML only and shows an actionable error for the describe text.
	DescribeService *describe.Service
	// KindRegistry lists every resource kind known to the cmdbar,
	// including pods. The default home view is whichever kind matches
	// HomeKind (typically "po").
	KindRegistry *kinds.Registry
	// ResourceServices is keyed by Plugin.Key. Includes "po"; pods
	// have no special path anymore.
	ResourceServices map[string]*kube.ResourceService
	// HomeKind is the kind shown at startup and returned to on `:po`
	// or on namespace switch. Typically "po".
	HomeKind string
	// HomeCh / HomeDone are the pre-Switched channels for HomeKind so
	// the first paint shows live data without waiting for a Switch()
	// call inside the bubbletea loop.
	HomeCh   <-chan []runtime.Object
	HomeDone <-chan struct{}
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

	active ActiveView
	// diagnoseOrigin is the view to return to when the diagnose
	// modal closes. ViewResource for `?`-on-pod; ViewLogs for
	// `?`-on-log-line.
	diagnoseOrigin ActiveView
	logsView       views.LogsView
	diagnoseView   views.DiagnoseView
	describeView   views.DescribeView
	cmdbar         components.Cmdbar

	logService      *kube.LogService
	podContextBldr  *diagnose.PodContextBuilder
	logContextBldr  *diagnose.LogContextBuilder
	toolDispatcher  *diagnose.Dispatcher
	aiProvider      ai.Provider
	aiModel         string
	describeService *describe.Service

	logsCh           <-chan []string
	logsDone         <-chan struct{}
	logsPod          *corev1.Pod
	logsContainer    string
	diagnoseSession  *diagnose.Session
	diagnoseEventsCh <-chan ai.Event

	registry         *kinds.Registry
	resourceServices map[string]*kube.ResourceService
	resourceView     views.ResourceView
	resourceCh       <-chan []runtime.Object
	resourceDone     <-chan struct{}
	resourceKind     string
	homeKind         string

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
	homeKind := deps.HomeKind
	if homeKind == "" {
		homeKind = "po"
	}

	m := Model{
		ctx:              parentCtx,
		flags:            deps.Flags,
		profile:          deps.Profile,
		mode:             "RO",
		contextName:      ctxName,
		namespace:        ns,
		active:           ViewResource,
		logsView:         views.NewLogsView(th),
		diagnoseView:     views.NewDiagnoseView(th),
		describeView:     views.NewDescribeView(th),
		cmdbar:           components.NewCmdbar(th),
		logService:       deps.LogService,
		podContextBldr:   deps.PodContextBuilder,
		logContextBldr:   deps.LogContextBuilder,
		toolDispatcher:   deps.ToolDispatcher,
		aiProvider:       deps.AIProvider,
		aiModel:          deps.AIModel,
		describeService:  deps.DescribeService,
		registry:         deps.KindRegistry,
		resourceServices: deps.ResourceServices,
		homeKind:         homeKind,
		resourceKind:     homeKind,
		resourceCh:       deps.HomeCh,
		resourceDone:     deps.HomeDone,
		footer:           components.NewFooter(th),
		keys:             DefaultKeyMap(),
		th:               th,
	}
	// Mount the home view if we know the plugin. Lazy fallback if
	// registry was never wired (tests).
	if deps.KindRegistry != nil {
		if p, ok := deps.KindRegistry.Lookup(homeKind); ok {
			m.resourceView = views.NewTableResourceView(th, p)
		}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if m.resourceCh == nil {
		return nil
	}
	return waitForResource(m.resourceCh, m.resourceDone, m.resourceKind)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
		m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
		m.describeView = m.describeView.SetSize(m.width, m.contentHeight())
		if m.resourceView != nil {
			m.resourceView = m.resourceView.SetSize(m.width, m.contentHeight())
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

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

	case DiagnosisToolCallMsg:
		m.diagnoseView = m.diagnoseView.AppendToolCall(msg.Name)
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

	case ResourceSnapshotMsg:
		// Stale-kind guard: a switch happened between snapshot emission
		// and Update receipt. Drop the snapshot, don't re-pump (the new
		// kind has its own pump).
		if m.resourceView == nil || msg.Kind != m.resourceKind {
			return m, nil
		}
		m.resourceView = m.resourceView.SetObjects(msg.Objects)
		return m, waitForResource(m.resourceCh, m.resourceDone, m.resourceKind)

	case ResourceStreamEndMsg:
		if msg.Kind == m.resourceKind {
			m.resourceCh = nil
			m.resourceDone = nil
		}
		return m, nil

	case DescribeReadyMsg:
		m.describeView = m.describeView.SetDescribe(msg.Text)
		return m, nil

	case DescribeErrorMsg:
		m.describeView = m.describeView.MarkError(msg.Err)
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
	case ViewResource:
		switch msg.String() {
		case "enter":
			return m.openDescribeForResource()
		case "esc":
			// On the home kind, esc is a no-op (we're already at
			// the top). On any other kind, esc returns to home.
			if m.resourceKind == m.homeKind {
				return m, nil
			}
			return m.switchToHome()
		}
		// Pod-specific keys are gated on the pod kind. M7 will
		// generalise `?` (and possibly `l`) to other kinds via the
		// kind-aware context builders.
		if m.resourceKind == "po" {
			switch msg.String() {
			case "l":
				return m.openLogs()
			case "?":
				return m.openDiagnose()
			}
		}
		var cmd tea.Cmd
		m.resourceView, cmd = m.resourceView.Update(msg)
		return m, cmd

	case ViewLogs:
		switch msg.String() {
		case "esc":
			return m.closeLogs()
		case "f":
			m.logsView = m.logsView.ToggleFollow()
			return m, nil
		case "?":
			return m.openDiagnoseForLog()
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

	case ViewDescribe:
		switch msg.String() {
		case "esc":
			return m.closeDescribe()
		case "y":
			m.describeView = m.describeView.ToggleMode()
			return m, nil
		}
		var cmd tea.Cmd
		m.describeView, cmd = m.describeView.Update(msg)
		return m, cmd
	}
	return m, nil
}

// selectedPod is the typed accessor for the pod path's logs/diagnose
// flows. Returns nil when the active kind isn't pods, the view is
// empty, or the selected object somehow isn't a Pod (defensive — the
// "po" plugin only ever stores *corev1.Pod).
func (m Model) selectedPod() *corev1.Pod {
	if m.resourceKind != "po" || m.resourceView == nil {
		return nil
	}
	obj := m.resourceView.SelectedObject()
	pod, _ := obj.(*corev1.Pod)
	return pod
}

func (m Model) openLogs() (Model, tea.Cmd) {
	pod := m.selectedPod()
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
	m.logsPod = pod
	// Empty container means "the only container" to LogService.Stream;
	// the diagnose payload reports it as "" which the builder handles
	// by omitting the line.
	m.logsContainer = ""
	return m, waitForLogs(m.logsCh, m.logsDone)
}

func (m Model) closeLogs() (Model, tea.Cmd) {
	if m.logService != nil {
		m.logService.Stop()
	}
	m.active = ViewResource
	m.logsCh = nil
	m.logsDone = nil
	m.logsPod = nil
	m.logsContainer = ""
	return m, nil
}

// openDiagnoseForLog is the `?` handler on ViewLogs. Pulls the cursor
// line + ±LogContextDefaultWindow window from the LogsView, builds a
// log-focused payload, and starts a diagnose session with the
// log-flow system prompt. Falls through to a no-op if the cursor is
// somehow invalid (empty buffer) or the AI / context builder isn't
// wired.
func (m Model) openDiagnoseForLog() (Model, tea.Cmd) {
	if m.logsPod == nil {
		return m, nil
	}
	line, idx, ok := m.logsView.SelectedLine()
	if !ok {
		return m, nil
	}
	window := m.logsView.LinesAround(idx, diagnose.LogContextDefaultWindow, diagnose.LogContextDefaultWindow)
	windowStart := idx - diagnose.LogContextDefaultWindow
	if windowStart < 0 {
		windowStart = 0
	}
	focus := diagnose.LogFocus{
		Line:        line,
		Index:       idx,
		Window:      window,
		WindowStart: windowStart,
	}

	m.diagnoseOrigin = ViewLogs
	m.active = ViewDiagnose
	m.diagnoseView = m.diagnoseView.Reset()
	m.diagnoseView = m.diagnoseView.SetTitle(fmt.Sprintf("%s/%s · log line %d",
		m.logsPod.Namespace, m.logsPod.Name, idx))
	m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())

	if m.aiProvider == nil || m.logContextBldr == nil {
		err := errors.New("AI not configured (set ANTHROPIC_API_KEY and restart)")
		m.diagnoseView = m.diagnoseView.MarkError(err)
		m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
		return m, nil
	}

	return m, startLogDiagnosis(m.ctx, m.logContextBldr, m.toolDispatcher, m.aiProvider, m.aiModel,
		m.logsPod, m.logsContainer, focus)
}

func (m Model) openDiagnose() (Model, tea.Cmd) {
	pod := m.selectedPod()
	if pod == nil {
		return m, nil
	}
	m.diagnoseOrigin = ViewResource
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
	// Return to wherever `?` was pressed (pod table or logs view).
	// Defaults to ViewResource (pod table) when origin wasn't set.
	if m.diagnoseOrigin == ViewLogs {
		m.active = ViewLogs
	} else {
		m.active = ViewResource
	}
	m.diagnoseOrigin = ViewResource
	return m, nil
}

func (m *Model) resizeAll() {
	m.logsView = m.logsView.SetSize(m.width, m.contentHeight())
	m.diagnoseView = m.diagnoseView.SetSize(m.width, m.contentHeight())
	m.describeView = m.describeView.SetSize(m.width, m.contentHeight())
	if m.resourceView != nil {
		m.resourceView = m.resourceView.SetSize(m.width, m.contentHeight())
	}
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
	if len(parts) == 1 {
		key := parts[0]
		if m.registry != nil {
			if p, ok := m.registry.Lookup(key); ok {
				m.cmdbar = m.cmdbar.Deactivate()
				m.resizeAll()
				return m.switchToResource(p)
			}
		}
	}
	m.cmdbar = m.cmdbar.SetError("unknown: " + input)
	return m, nil
}

// switchToResource (re)mounts the view for a kind. Tears down any
// stacked modal, calls Switch on the per-kind service to bind a
// fresh informer in the current namespace, and starts the pump.
// Pods is just another kind — no special path.
func (m Model) switchToResource(p kinds.Plugin) (Model, tea.Cmd) {
	if m.resourceServices == nil {
		m.cmdbar = m.cmdbar.Activate().SetError("resource services not configured")
		return m, nil
	}
	svc, ok := m.resourceServices[p.Key]
	if !ok {
		m.cmdbar = m.cmdbar.Activate().SetError("no service for kind " + p.Key)
		return m, nil
	}
	ch, done, err := svc.Switch(m.namespace)
	if err != nil {
		m.cmdbar = m.cmdbar.Activate().SetError("switch failed: " + err.Error())
		return m, nil
	}

	view := views.NewTableResourceView(m.th, p)
	m.resourceView = view.SetSize(m.width, m.contentHeight())
	m.resourceCh = ch
	m.resourceDone = done
	m.resourceKind = p.Key
	m.active = ViewResource
	// Drop any stacked modal — its selection was bound to the prior
	// kind and is meaningless now.
	m.logsCh, m.logsDone, m.diagnoseEventsCh = nil, nil, nil
	if m.diagnoseSession != nil {
		m.diagnoseSession.Cancel()
		m.diagnoseSession = nil
	}
	return m, waitForResource(ch, done, p.Key)
}

// switchToHome returns to the home kind (typically pods). Used by
// esc on a non-home kind and by namespace change. Looks up the home
// plugin from the registry and delegates to switchToResource.
func (m Model) switchToHome() (Model, tea.Cmd) {
	if m.registry == nil {
		return m, nil
	}
	p, ok := m.registry.Lookup(m.homeKind)
	if !ok {
		return m, nil
	}
	return m.switchToResource(p)
}

// openDescribeForResource is the Enter handler on ViewResource.
// Works for every kind including pods (post-refactor pods are just
// another kind).
func (m Model) openDescribeForResource() (Model, tea.Cmd) {
	if m.resourceView == nil || m.registry == nil {
		return m, nil
	}
	obj := m.resourceView.SelectedObject()
	if obj == nil {
		return m, nil
	}
	plugin, ok := m.registry.Lookup(m.resourceKind)
	if !ok {
		return m, nil
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return m, nil
	}
	return m.openDescribe(plugin, obj, accessor.GetNamespace(), accessor.GetName())
}

// openDescribe is the shared modal-open path. Both Enter handlers
// converge here. Sets YAML synchronously off the cached object, kicks
// off a Cmd to stream in the kubectl/describe text, and routes to
// ViewDescribe. Cluster-scoped objects (empty namespace) get a
// shortened title.
func (m Model) openDescribe(plugin kinds.Plugin, obj runtime.Object, namespace, name string) (Model, tea.Cmd) {
	title := fmt.Sprintf("%s/%s · %s", namespace, name, plugin.Kind)
	if namespace == "" {
		title = fmt.Sprintf("%s · %s", name, plugin.Kind)
	}

	m.describeView = m.describeView.Reset().SetTitle(title)
	if m.describeService != nil {
		if y, err := m.describeService.YAML(obj, plugin); err == nil {
			m.describeView = m.describeView.SetYAML(y)
		}
	}
	m.describeView = m.describeView.SetSize(m.width, m.contentHeight())
	m.active = ViewDescribe

	if m.describeService == nil {
		m.describeView = m.describeView.MarkError(errors.New("no REST config; describe unavailable"))
		return m, nil
	}
	return m, fetchDescribe(m.ctx, m.describeService, plugin, namespace, name)
}

// closeDescribe returns to the resource view (the only view the
// modal can be opened from). The view's snapshot pump wasn't
// touched, so it picks up where it left off.
func (m Model) closeDescribe() (Model, tea.Cmd) {
	m.active = ViewResource
	return m, nil
}

// fetchDescribe runs describe.Service.Describe off the bubbletea
// main loop and produces a DescribeReadyMsg / DescribeErrorMsg.
func fetchDescribe(ctx context.Context, svc *describe.Service, plugin kinds.Plugin, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		text, err := svc.Describe(ctx, plugin, namespace, name)
		if err != nil {
			return DescribeErrorMsg{Err: err}
		}
		return DescribeReadyMsg{Text: text}
	}
}

func (m Model) handleNamespaceChange(namespace string) (Model, tea.Cmd) {
	m.namespace = namespace
	// Drop any stacked modal — pod logs and diagnose were bound to
	// the prior namespace's selection and are now meaningless.
	if m.logService != nil {
		m.logService.Stop()
	}
	if m.diagnoseSession != nil {
		m.diagnoseSession.Cancel()
		m.diagnoseSession = nil
	}
	m.logsCh, m.logsDone, m.diagnoseEventsCh = nil, nil, nil
	// Re-switch the home kind on the new namespace. switchToResource
	// reads m.namespace, so the new value lands automatically.
	return m.switchToHome()
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
	case ViewDescribe:
		content = m.describeView.View()
	default:
		if m.resourceView != nil {
			content = m.resourceView.View()
		}
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
	case ViewDescribe:
		return "y toggle · esc back · q quit"
	case ViewResource:
		if m.resourceKind == "po" {
			return "l logs · ↵ describe · ? AI · q quit · : cmd"
		}
		return "esc back · ↵ describe · q quit · : cmd"
	}
	return ""
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

// startLogDiagnosis is the log-focused counterpart to startDiagnosis.
// Builds a payload anchored on a specific log line (with surrounding
// window + pod context) and opens a session with the log system prompt.
// Same Cmd-then-message shape as startDiagnosis so the existing
// DiagnosisStartedMsg / DiagnosisDeltaMsg / DiagnosisToolCallMsg
// pumping logic just works.
func startLogDiagnosis(
	ctx context.Context,
	builder *diagnose.LogContextBuilder,
	dispatcher *diagnose.Dispatcher,
	provider ai.Provider,
	model string,
	pod *corev1.Pod,
	container string,
	focus diagnose.LogFocus,
) tea.Cmd {
	return func() tea.Msg {
		payload := builder.Build(ctx, pod, container, focus)
		session := diagnose.StartWithPrompt(ctx, provider, model, payload, diagnose.LogSystemPrompt, dispatcher)
		return DiagnosisStartedMsg{Session: session, Pod: pod}
	}
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
			// Must surface a real message: a nil return ends the Cmd
			// without re-issuing waitForDiagnoseEvent, which stalls the
			// pump and wedges the session (see DiagnosisToolCallMsg).
			return DiagnosisToolCallMsg{Name: e.Name}
		}
		return nil
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

// waitForResource pumps one snapshot from the active resource kind's
// channel into a ResourceSnapshotMsg. Channel-close and done-close
// both end the stream; Kind lets Update drop stale snapshots after a
// kind switch.
func waitForResource(ch <-chan []runtime.Object, done <-chan struct{}, kind string) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		select {
		case objs, ok := <-ch:
			if !ok {
				return ResourceStreamEndMsg{Kind: kind}
			}
			return ResourceSnapshotMsg{Kind: kind, Objects: objs}
		case <-done:
			return ResourceStreamEndMsg{Kind: kind}
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
