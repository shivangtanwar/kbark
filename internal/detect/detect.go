// SPDX-License-Identifier: Apache-2.0

// Package detect turns observable Kubernetes pod state into typed
// failure findings. Everything here is a pure function of its inputs:
// no API calls, no clock reads, no AI. That is the point — whether a
// pod is OOM-killed is a fact kbark can establish itself, and the
// autofix layer acts only on facts established here.
package detect

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Family is a class of pod failure with its own detector and playbook.
type Family string

const (
	FamilyOOM        Family = "oom"
	FamilyCrash      Family = "crash"
	FamilyImagePull  Family = "image-pull"
	FamilyConfig     Family = "config"
	FamilyProbe      Family = "probe"
	FamilyScheduling Family = "scheduling"
	FamilyStorage    Family = "storage"
	FamilyEvicted    Family = "evicted"
	FamilyNetwork    Family = "network"
)

// Causes refine a family. The playbook branches on them, so they are
// constants rather than free text.
const (
	CauseBadReference = "bad-reference" // image name or tag does not exist
	CauseAuth         = "auth"          // registry refused credentials
	CauseRegistry     = "registry"      // registry unreachable

	CauseProbeTimeout   = "timeout"
	CauseProbeRefused   = "refused"
	CauseProbeStatus    = "status"
	CauseProbeReadiness = "readiness"

	CauseCordoned     = "cordoned"
	CauseInsufficient = "insufficient"
	CauseSelector     = "selector"
	CauseTaint        = "taint"

	// CauseInferred marks an out-of-memory finding that the container
	// runtime did not label as OOMKilled. Some runtimes (containerd
	// inside Kind, for one) report such kills as a plain exit 137.
	CauseInferred = "inferred"

	CauseUnknown = "unknown"
)

// Windows that decide whether a past signal still counts as current.
const (
	// RecentWindow is how long a termination or event keeps a finding
	// alive after the container has restarted. A crash-looping
	// container spends most of its time between crashes; without this
	// the finding would flap every restart.
	RecentWindow = 10 * time.Minute
	// PendingGrace is how long a pod may be unschedulable or stuck
	// creating before it is reported. Short pending periods are normal.
	PendingGrace = 20 * time.Second
	// NotReadyGrace is how long a running container may be unready
	// before a failing readiness probe is reported.
	NotReadyGrace = 60 * time.Second
)

// Evidence is one observed fact supporting a finding.
type Evidence struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Finding is one detected failure on one pod.
type Finding struct {
	Family    Family     `json:"family"`
	Reason    string     `json:"reason"`
	Cause     string     `json:"cause,omitempty"`
	Summary   string     `json:"summary"`
	Container string     `json:"container,omitempty"`
	Init      bool       `json:"init,omitempty"`
	Probe     string     `json:"probe,omitempty"` // liveness | startup | readiness
	Evidence  []Evidence `json:"evidence"`
}

// Pod returns the findings for one pod. events are the events whose
// involved object is this pod, in any order. A healthy pod returns nil.
func Pod(pod *corev1.Pod, events []*corev1.Event, now time.Time) []Finding {
	if pod == nil || pod.DeletionTimestamp != nil {
		return nil
	}
	if pod.Status.Phase == corev1.PodSucceeded {
		return nil
	}
	if f, ok := evicted(pod); ok {
		return []Finding{f}
	}
	if pod.Status.Phase == corev1.PodFailed {
		return nil
	}
	if f, ok := scheduling(pod, now); ok {
		return []Finding{f}
	}

	var out []Finding
	seen := map[Family]bool{}
	add := func(f Finding) {
		if !seen[f.Family] {
			seen[f.Family] = true
			out = append(out, f)
		}
	}
	for i := range pod.Status.InitContainerStatuses {
		if f, ok := container(pod, &pod.Status.InitContainerStatuses[i], true, events, now); ok {
			add(f)
		}
	}
	for i := range pod.Status.ContainerStatuses {
		if f, ok := container(pod, &pod.Status.ContainerStatuses[i], false, events, now); ok {
			add(f)
		}
	}
	if len(out) == 0 {
		if f, ok := mount(pod, events, now); ok {
			add(f)
		}
	}
	return out
}

func evicted(pod *corev1.Pod) (Finding, bool) {
	if pod.Status.Phase != corev1.PodFailed || pod.Status.Reason == "" {
		return Finding{}, false
	}
	for _, o := range pod.OwnerReferences {
		if o.Kind == "Job" {
			// A failed Job pod is the Job's own record; the Job
			// controller decides what happens to it.
			return Finding{}, false
		}
	}
	ev := []Evidence{
		{"Pod phase", "Failed"},
		{"Reason", pod.Status.Reason},
	}
	if pod.Status.Message != "" {
		ev = append(ev, Evidence{"Message", pod.Status.Message})
	}
	if pod.Spec.NodeName != "" {
		ev = append(ev, Evidence{"Node", pod.Spec.NodeName})
	}
	return Finding{
		Family:   FamilyEvicted,
		Reason:   pod.Status.Reason,
		Summary:  fmt.Sprintf("pod was terminated by the node (%s) and its dead record remains", pod.Status.Reason),
		Evidence: ev,
	}, true
}

func scheduling(pod *corev1.Pod, now time.Time) (Finding, bool) {
	if pod.Status.Phase != corev1.PodPending || pod.Spec.NodeName != "" {
		return Finding{}, false
	}
	var cond *corev1.PodCondition
	for i := range pod.Status.Conditions {
		c := &pod.Status.Conditions[i]
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			cond = c
		}
	}
	if cond == nil || now.Sub(pod.CreationTimestamp.Time) < PendingGrace {
		return Finding{}, false
	}
	msg := cond.Message
	lower := strings.ToLower(msg)
	f := Finding{
		Reason: "Unschedulable",
		Evidence: []Evidence{
			{"Pod phase", "Pending"},
			{"Scheduler message", msg},
			{"Pending for", now.Sub(pod.CreationTimestamp.Time).Round(time.Second).String()},
		},
	}
	switch {
	case strings.Contains(lower, "persistentvolumeclaim"), strings.Contains(lower, "volume node affinity"):
		f.Family, f.Cause = FamilyStorage, CauseUnknown
		f.Reason = "VolumeUnavailable"
		f.Summary = "pod cannot be scheduled because a volume it needs is not available"
		return f, true
	case strings.Contains(lower, "unschedulable"):
		f.Cause = CauseCordoned
		f.Summary = "pod cannot be scheduled because a node is cordoned"
	case strings.Contains(lower, "insufficient"):
		f.Cause = CauseInsufficient
		f.Summary = "pod cannot be scheduled because no node has enough free CPU or memory"
		f.Evidence = append(f.Evidence, Evidence{"Requests", requests(pod)})
	case strings.Contains(lower, "affinity"), strings.Contains(lower, "selector"):
		f.Cause = CauseSelector
		f.Summary = "pod cannot be scheduled because its node selector or affinity matches no node"
		if len(pod.Spec.NodeSelector) > 0 {
			f.Evidence = append(f.Evidence, Evidence{"Node selector", labels(pod.Spec.NodeSelector)})
		}
	case strings.Contains(lower, "taint"):
		f.Cause = CauseTaint
		f.Summary = "pod cannot be scheduled because it does not tolerate the nodes' taints"
	default:
		f.Cause = CauseUnknown
		f.Summary = "pod cannot be scheduled"
	}
	f.Family = FamilyScheduling
	return f, true
}

var imagePullReasons = map[string]bool{
	"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true, "ErrImageNeverPull": true,
}

func container(pod *corev1.Pod, cs *corev1.ContainerStatus, init bool, events []*corev1.Event, now time.Time) (Finding, bool) {
	spec := containerSpec(pod, cs.Name, init)
	w := cs.State.Waiting
	term := cs.State.Terminated
	if term == nil {
		term = cs.LastTerminationState.Terminated
	}
	base := Finding{Container: cs.Name, Init: init}
	kind := "container"
	if init {
		kind = "init container"
	}

	// Image pull: the container has never started.
	if w != nil && imagePullReasons[w.Reason] {
		text := w.Message + " " + eventText(events, cs.Name, now, "Failed", "InspectFailed")
		f := base
		f.Family, f.Reason, f.Cause = FamilyImagePull, w.Reason, imageCause(w.Reason, text)
		f.Summary = fmt.Sprintf("%s %q cannot pull image %s", kind, cs.Name, cs.Image)
		f.Evidence = []Evidence{
			{"Container state", "Waiting: " + w.Reason},
			{"Image", cs.Image},
		}
		if w.Message != "" {
			f.Evidence = append(f.Evidence, Evidence{"Kubelet message", w.Message})
		}
		return f, true
	}

	// Missing ConfigMap, Secret or key: the kubelet cannot build the container.
	if w != nil && w.Reason == "CreateContainerConfigError" {
		f := base
		f.Family, f.Reason = FamilyConfig, w.Reason
		missing := missingConfig(w.Message)
		f.Summary = fmt.Sprintf("%s %q cannot start: %s", kind, cs.Name, orDefault(missing, "a referenced ConfigMap or Secret is missing"))
		f.Evidence = []Evidence{
			{"Container state", "Waiting: " + w.Reason},
			{"Kubelet message", w.Message},
		}
		if missing != "" {
			f.Evidence = append(f.Evidence, Evidence{"Missing", missing})
		}
		return f, true
	}

	stillFailing := !cs.Ready || (term != nil && now.Sub(term.FinishedAt.Time) < RecentWindow)

	// Out of memory.
	if term != nil && term.Reason == "OOMKilled" && stillFailing && (init || cs.RestartCount > 0 || cs.State.Terminated != nil) {
		f := base
		f.Family, f.Reason = FamilyOOM, "OOMKilled"
		limit := "none"
		if spec != nil {
			if q, ok := spec.Resources.Limits[corev1.ResourceMemory]; ok {
				limit = q.String()
			}
		}
		f.Summary = fmt.Sprintf("%s %q was killed for exceeding its memory limit (%s)", kind, cs.Name, limit)
		f.Evidence = []Evidence{
			{"Last termination", fmt.Sprintf("OOMKilled, exit code %d", term.ExitCode)},
			{"Memory limit", limit},
			{"Restarts", fmt.Sprint(cs.RestartCount)},
		}
		if spec != nil {
			if q, ok := spec.Resources.Requests[corev1.ResourceMemory]; ok {
				f.Evidence = append(f.Evidence, Evidence{"Memory request", q.String()})
			}
		}
		if !term.FinishedAt.IsZero() {
			f.Evidence = append(f.Evidence, Evidence{"Killed at", term.FinishedAt.UTC().Format(time.RFC3339)})
		}
		return f, true
	}

	// Liveness or startup probe kills. The kubelet sends SIGTERM then
	// SIGKILL, so the exit code is a signal exit (or a clean 0), which
	// separates this from an application that crashes on its own.
	if !init && cs.RestartCount > 0 && stillFailing {
		if ev, probe := probeEvent(events, cs.Name, now, "Liveness", "Startup"); ev != nil {
			if term == nil || term.ExitCode == 137 || term.ExitCode == 143 || term.ExitCode == 0 {
				f := base
				f.Family, f.Reason, f.Probe = FamilyProbe, "Unhealthy", strings.ToLower(probe)
				f.Cause = probeCause(ev.Message)
				f.Summary = fmt.Sprintf("container %q is being restarted because its %s probe fails", cs.Name, f.Probe)
				f.Evidence = []Evidence{
					{"Probe event", fmt.Sprintf("%s (seen %d times)", ev.Message, eventCount(ev))},
					{"Restarts", fmt.Sprint(cs.RestartCount)},
				}
				if spec != nil {
					if p := probeOf(spec, f.Probe); p != nil {
						f.Evidence = append(f.Evidence, Evidence{capitalize(f.Probe) + " probe", describeProbe(p)})
					}
				}
				if term != nil {
					f.Evidence = append(f.Evidence, Evidence{"Last termination", fmt.Sprintf("exit code %d (%s)", term.ExitCode, term.Reason)})
				}
				return f, true
			}
		}
	}

	// Probable out-of-memory kill that the runtime reported as a plain
	// SIGKILL. Three things must hold: exit code 137, a memory limit on
	// the container, and (checked above) no probe kill to explain it.
	repeating := cs.RestartCount >= 1 || (w != nil && w.Reason == "CrashLoopBackOff")
	if term != nil && term.ExitCode == 137 && term.Reason != "OOMKilled" && repeating && stillFailing && spec != nil {
		if limit, ok := spec.Resources.Limits[corev1.ResourceMemory]; ok && !limit.IsZero() {
			f := base
			f.Family, f.Reason, f.Cause = FamilyOOM, "Killed", CauseInferred
			f.Summary = fmt.Sprintf("%s %q is repeatedly killed (exit 137) under a %s memory limit; an out-of-memory kill is the likely cause", kind, cs.Name, limit.String())
			f.Evidence = []Evidence{
				{"Last termination", fmt.Sprintf("exit code 137, SIGKILL (%s)", term.Reason)},
				{"Memory limit", limit.String()},
				{"Restarts", fmt.Sprint(cs.RestartCount)},
				{"Probe kills", "none recorded"},
				{"Basis", "inferred: the container runtime did not report OOMKilled"},
			}
			if !term.StartedAt.IsZero() && !term.FinishedAt.IsZero() {
				f.Evidence = append(f.Evidence, Evidence{"Last run lasted", term.FinishedAt.Sub(term.StartedAt.Time).Round(time.Second).String()})
			}
			return f, true
		}
	}

	// Application crash.
	crashWaiting := w != nil && (w.Reason == "CrashLoopBackOff" || w.Reason == "RunContainerError" || w.Reason == "CreateContainerError")
	crashRepeating := term != nil && term.ExitCode != 0 && cs.RestartCount >= 2 && stillFailing
	initFailed := init && cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0
	if crashWaiting || crashRepeating || initFailed {
		f := base
		f.Family = FamilyCrash
		f.Reason = "CrashLoopBackOff"
		if w != nil && w.Reason != "" {
			f.Reason = w.Reason
		} else if initFailed {
			f.Reason = "Error"
		}
		f.Evidence = []Evidence{{"Restarts", fmt.Sprint(cs.RestartCount)}}
		if w != nil {
			f.Evidence = append([]Evidence{{"Container state", "Waiting: " + w.Reason}}, f.Evidence...)
			if w.Message != "" && w.Reason != "CrashLoopBackOff" {
				f.Evidence = append(f.Evidence, Evidence{"Kubelet message", w.Message})
			}
		}
		if term != nil {
			f.Summary = fmt.Sprintf("%s %q keeps exiting with code %d", kind, cs.Name, term.ExitCode)
			f.Evidence = append(f.Evidence, Evidence{"Last termination", fmt.Sprintf("exit code %d (%s)", term.ExitCode, term.Reason)})
		} else {
			f.Summary = fmt.Sprintf("%s %q fails to start (%s)", kind, cs.Name, f.Reason)
		}
		f.Evidence = append(f.Evidence, Evidence{"Image", cs.Image})
		return f, true
	}

	// Readiness: running but never becoming ready.
	if !init && cs.State.Running != nil && !cs.Ready && now.Sub(cs.State.Running.StartedAt.Time) > NotReadyGrace {
		if ev, _ := probeEvent(events, cs.Name, now, "Readiness"); ev != nil {
			f := base
			f.Family, f.Reason, f.Probe, f.Cause = FamilyProbe, "Unhealthy", "readiness", CauseProbeReadiness
			f.Summary = fmt.Sprintf("container %q is running but its readiness probe fails, so it receives no traffic", cs.Name)
			f.Evidence = []Evidence{
				{"Probe event", fmt.Sprintf("%s (seen %d times)", ev.Message, eventCount(ev))},
				{"Not ready for", now.Sub(cs.State.Running.StartedAt.Time).Round(time.Second).String()},
			}
			if spec != nil && spec.ReadinessProbe != nil {
				f.Evidence = append(f.Evidence, Evidence{"Readiness probe", describeProbe(spec.ReadinessProbe)})
			}
			return f, true
		}
	}
	return Finding{}, false
}

// mount reports a pod stuck creating because a volume cannot be set up.
func mount(pod *corev1.Pod, events []*corev1.Event, now time.Time) (Finding, bool) {
	if pod.Status.Phase != corev1.PodPending || pod.Spec.NodeName == "" {
		return Finding{}, false
	}
	if now.Sub(pod.CreationTimestamp.Time) < PendingGrace {
		return Finding{}, false
	}
	var ev *corev1.Event
	for _, e := range events {
		if (e.Reason == "FailedMount" || e.Reason == "FailedAttachVolume") && now.Sub(eventTime(e)) < RecentWindow {
			if ev == nil || eventTime(e).After(eventTime(ev)) {
				ev = e
			}
		}
	}
	if ev == nil {
		return Finding{}, false
	}
	f := Finding{
		Reason: ev.Reason,
		Evidence: []Evidence{
			{"Pod phase", "Pending (ContainerCreating)"},
			{"Event", fmt.Sprintf("%s: %s (seen %d times)", ev.Reason, ev.Message, eventCount(ev))},
		},
	}
	if missing := missingConfig(ev.Message); missing != "" {
		f.Family = FamilyConfig
		f.Summary = "pod cannot start: " + missing
		f.Evidence = append(f.Evidence, Evidence{"Missing", missing})
		return f, true
	}
	f.Family, f.Cause = FamilyStorage, CauseUnknown
	f.Summary = "pod cannot start because a volume cannot be mounted or attached"
	return f, true
}

// --- helpers ---

func containerSpec(pod *corev1.Pod, name string, init bool) *corev1.Container {
	list := pod.Spec.Containers
	if init {
		list = pod.Spec.InitContainers
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

func imageCause(reason, text string) string {
	t := strings.ToLower(text)
	switch {
	case reason == "InvalidImageName":
		return CauseBadReference
	case strings.Contains(t, "not found"), strings.Contains(t, "manifest unknown"), strings.Contains(t, "repository does not exist"):
		return CauseBadReference
	case strings.Contains(t, "unauthorized"), strings.Contains(t, "denied"), strings.Contains(t, "authentication required"), strings.Contains(t, "authorization failed"):
		return CauseAuth
	case strings.Contains(t, "timeout"), strings.Contains(t, "no such host"), strings.Contains(t, "connection refused"), strings.Contains(t, "toomanyrequests"):
		return CauseRegistry
	}
	return CauseUnknown
}

func probeCause(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "deadline exceeded"), strings.Contains(m, "timeout"), strings.Contains(m, "timed out"):
		return CauseProbeTimeout
	case strings.Contains(m, "connection refused"), strings.Contains(m, "connect: "):
		return CauseProbeRefused
	case strings.Contains(m, "statuscode"):
		return CauseProbeStatus
	}
	return CauseUnknown
}

var (
	reMissingObject = regexp.MustCompile(`(?i)(configmap|secret) "([^"]+)" not found`)
	reMissingKey    = regexp.MustCompile(`(?i)couldn't find key (\S+) in (ConfigMap|Secret) (\S+)`)
)

// missingConfig extracts the missing object or key from a kubelet
// message, or returns "" when the message has neither shape.
func missingConfig(msg string) string {
	if m := reMissingKey.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("key %q is missing from %s %s", m[1], m[2], m[3])
	}
	if m := reMissingObject.FindStringSubmatch(msg); m != nil {
		kind := "ConfigMap"
		if strings.EqualFold(m[1], "secret") {
			kind = "Secret"
		}
		return fmt.Sprintf("%s %q does not exist", kind, m[2])
	}
	return ""
}

// probeEvent returns the most recent Unhealthy event for the container
// whose message names one of the given probe types.
func probeEvent(events []*corev1.Event, containerName string, now time.Time, probes ...string) (*corev1.Event, string) {
	var best *corev1.Event
	var bestProbe string
	for _, e := range events {
		if e.Reason != "Unhealthy" || !forContainer(e, containerName) || now.Sub(eventTime(e)) > RecentWindow {
			continue
		}
		for _, p := range probes {
			if strings.HasPrefix(e.Message, p+" probe failed") {
				if best == nil || eventTime(e).After(eventTime(best)) {
					best, bestProbe = e, p
				}
			}
		}
	}
	return best, bestProbe
}

func eventText(events []*corev1.Event, containerName string, now time.Time, reasons ...string) string {
	var b strings.Builder
	for _, e := range events {
		if !forContainer(e, containerName) || now.Sub(eventTime(e)) > RecentWindow {
			continue
		}
		for _, r := range reasons {
			if e.Reason == r {
				b.WriteString(e.Message)
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}

func forContainer(e *corev1.Event, name string) bool {
	fp := e.InvolvedObject.FieldPath
	return fp == "" || strings.Contains(fp, "{"+name+"}")
}

func eventTime(e *corev1.Event) time.Time {
	t := e.LastTimestamp.Time
	if e.Series != nil && e.Series.LastObservedTime.After(t) {
		t = e.Series.LastObservedTime.Time
	}
	if e.EventTime.After(t) {
		t = e.EventTime.Time
	}
	if t.IsZero() {
		t = e.FirstTimestamp.Time
	}
	if t.IsZero() {
		t = e.CreationTimestamp.Time
	}
	return t
}

func eventCount(e *corev1.Event) int32 {
	if e.Series != nil && e.Series.Count > e.Count {
		return e.Series.Count
	}
	if e.Count == 0 {
		return 1
	}
	return e.Count
}

func probeOf(c *corev1.Container, kind string) *corev1.Probe {
	switch kind {
	case "liveness":
		return c.LivenessProbe
	case "startup":
		return c.StartupProbe
	case "readiness":
		return c.ReadinessProbe
	}
	return nil
}

func describeProbe(p *corev1.Probe) string {
	var target string
	switch {
	case p.HTTPGet != nil:
		target = fmt.Sprintf("HTTP GET %s on port %s", p.HTTPGet.Path, p.HTTPGet.Port.String())
	case p.TCPSocket != nil:
		target = "TCP port " + p.TCPSocket.Port.String()
	case p.Exec != nil:
		target = "exec " + strings.Join(p.Exec.Command, " ")
	case p.GRPC != nil:
		target = fmt.Sprintf("gRPC port %d", p.GRPC.Port)
	}
	// Zero means "unset"; show the Kubernetes default the kubelet uses.
	def := func(v, d int32) int32 {
		if v == 0 {
			return d
		}
		return v
	}
	return fmt.Sprintf("%s; initialDelay=%ds timeout=%ds period=%ds failureThreshold=%d",
		target, p.InitialDelaySeconds, def(p.TimeoutSeconds, 1), def(p.PeriodSeconds, 10), def(p.FailureThreshold, 3))
}

func requests(pod *corev1.Pod) string {
	var parts []string
	for _, c := range pod.Spec.Containers {
		cpu, mem := c.Resources.Requests[corev1.ResourceCPU], c.Resources.Requests[corev1.ResourceMemory]
		parts = append(parts, fmt.Sprintf("%s: cpu=%s memory=%s", c.Name, cpu.String(), mem.String()))
	}
	return strings.Join(parts, "; ")
}

func labels(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
