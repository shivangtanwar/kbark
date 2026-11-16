// SPDX-License-Identifier: Apache-2.0

// Package fleet watches several Kubernetes clusters at once. Each
// Cluster owns one clientset and one set of informers; Scan runs the
// detectors over the cached state and returns what is failing.
package fleet

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shivangtanwar/kbark/internal/detect"
)

const (
	resync        = 5 * time.Minute
	eventsByUID   = "involvedUID"
	statusSyncing = "connecting"
	statusReady   = "connected"
)

// Ref names an object inside a namespace.
type Ref struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (r Ref) String() string { return r.Kind + "/" + r.Name }

// Cluster is one watched cluster.
type Cluster struct {
	Name   string
	Client kubernetes.Interface

	factory informers.SharedInformerFactory
	events  cache.Indexer

	Pods         corelisters.PodLister
	Nodes        corelisters.NodeLister
	Services     corelisters.ServiceLister
	ReplicaSets  appslisters.ReplicaSetLister
	Deployments  appslisters.DeploymentLister
	StatefulSets appslisters.StatefulSetLister
	DaemonSets   appslisters.DaemonSetLister
	Revisions    appslisters.ControllerRevisionLister

	mu     sync.RWMutex
	synced bool
	err    string
}

// RESTConfigForContext loads the kubeconfig (honouring $KUBECONFIG, or
// the explicit path when non-empty) and returns the config for one
// named context.
func RESTConfigForContext(kubeconfigPath, contextName string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{CurrentContext: contextName},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", contextName, err)
	}
	return cfg, nil
}

// CurrentContext returns the kubeconfig's current context name.
func CurrentContext(kubeconfigPath string) (string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	}
	raw, err := rules.Load()
	if err != nil {
		return "", err
	}
	if raw.CurrentContext == "" {
		return "", fmt.Errorf("kubeconfig has no current context")
	}
	return raw.CurrentContext, nil
}

// Connect builds a Cluster from a rest.Config.
func Connect(name string, cfg *rest.Config) (*Cluster, error) {
	// The dashboard polls caches, not the API, so the client only
	// carries watches and the occasional write. Raise the defaults a
	// little so a planning burst is not throttled.
	cfg.QPS, cfg.Burst = 20, 40
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("cluster %q: %w", name, err)
	}
	return New(name, client), nil
}

// New builds a Cluster around an existing clientset. Tests pass a fake.
func New(name string, client kubernetes.Interface) *Cluster {
	f := informers.NewSharedInformerFactory(client, resync)
	c := &Cluster{Name: name, Client: client, factory: f}

	core, apps := f.Core().V1(), f.Apps().V1()
	c.Pods = core.Pods().Lister()
	c.Nodes = core.Nodes().Lister()
	c.Services = core.Services().Lister()
	c.ReplicaSets = apps.ReplicaSets().Lister()
	c.Deployments = apps.Deployments().Lister()
	c.StatefulSets = apps.StatefulSets().Lister()
	c.DaemonSets = apps.DaemonSets().Lister()
	c.Revisions = apps.ControllerRevisions().Lister()

	ev := core.Events().Informer()
	_ = ev.AddIndexers(cache.Indexers{eventsByUID: func(obj any) ([]string, error) {
		e, ok := obj.(*corev1.Event)
		if !ok {
			return nil, nil
		}
		return []string{string(e.InvolvedObject.UID)}, nil
	}})
	c.events = ev.GetIndexer()

	// Touch every informer so the factory starts it.
	for _, inf := range []cache.SharedIndexInformer{
		core.Pods().Informer(), core.Nodes().Informer(), core.Services().Informer(),
		apps.ReplicaSets().Informer(), apps.Deployments().Informer(),
		apps.StatefulSets().Informer(), apps.DaemonSets().Informer(), apps.ControllerRevisions().Informer(),
	} {
		_ = inf.SetWatchErrorHandler(c.watchError)
	}
	return c
}

func (c *Cluster) watchError(_ *cache.Reflector, err error) {
	c.mu.Lock()
	c.err = err.Error()
	c.mu.Unlock()
}

// Start launches the informers and returns immediately. Sync happens in
// the background so one unreachable cluster cannot hold up the rest;
// Status reports progress.
func (c *Cluster) Start(ctx context.Context) {
	c.factory.Start(ctx.Done())
	go func() {
		for _, ok := range c.factory.WaitForCacheSync(ctx.Done()) {
			if !ok {
				return
			}
		}
		c.mu.Lock()
		c.synced, c.err = true, ""
		c.mu.Unlock()
	}()
}

// WaitForSync blocks until the caches are warm or ctx ends.
func (c *Cluster) WaitForSync(ctx context.Context) bool {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		if c.Synced() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

// Synced reports whether the caches are warm.
func (c *Cluster) Synced() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.synced
}

// Status returns a short connection state and the last watch error.
func (c *Cluster) Status() (state, lastErr string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.synced {
		return statusReady, ""
	}
	return statusSyncing, c.err
}

// EventsFor returns the cached events whose involved object has uid.
func (c *Cluster) EventsFor(uid string) []*corev1.Event {
	objs, _ := c.events.ByIndex(eventsByUID, uid)
	out := make([]*corev1.Event, 0, len(objs))
	for _, o := range objs {
		if e, ok := o.(*corev1.Event); ok {
			out = append(out, e)
		}
	}
	return out
}

// Owner resolves the workload that controls a pod: a Deployment (via
// its ReplicaSet), StatefulSet, DaemonSet or Job. A bare pod is its own
// owner.
func (c *Cluster) Owner(pod *corev1.Pod) Ref {
	for _, o := range pod.OwnerReferences {
		if o.Controller == nil || !*o.Controller {
			continue
		}
		if o.Kind == "ReplicaSet" {
			if rs, err := c.ReplicaSets.ReplicaSets(pod.Namespace).Get(o.Name); err == nil {
				for _, ro := range rs.OwnerReferences {
					if ro.Controller != nil && *ro.Controller && ro.Kind == "Deployment" {
						return Ref{Kind: "Deployment", Name: ro.Name}
					}
				}
			}
		}
		return Ref{Kind: o.Kind, Name: o.Name}
	}
	return Ref{Kind: "Pod", Name: pod.Name}
}

// Observation is one failing workload: every pod of the workload that
// shows the same failure family is folded into a single observation.
type Observation struct {
	Key       string         `json:"key"`
	Namespace string         `json:"namespace"`
	Workload  Ref            `json:"workload"`
	Finding   detect.Finding `json:"finding"`
	Pods      []string       `json:"pods"`
}

// PodRow is one line of the dashboard's pod table.
type PodRow struct {
	Cluster   string   `json:"cluster"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Ready     string   `json:"ready"`
	Restarts  int32    `json:"restarts"`
	Created   string   `json:"created"`
	Node      string   `json:"node"`
	Owner     Ref      `json:"owner"`
	Health    string   `json:"health"` // healthy | failing | pending | done
	Families  []string `json:"families,omitempty"`
}

// Summary is the cluster tile on the dashboard.
type Summary struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	Nodes      int    `json:"nodes"`
	NodesReady int    `json:"nodesReady"`
	Cordoned   int    `json:"cordoned"`
	Namespaces int    `json:"namespaces"`
	Pods       int    `json:"pods"`
	Healthy    int    `json:"healthy"`
	Failing    int    `json:"failing"`
	Pending    int    `json:"pending"`
}

// Snapshot is the result of one Scan.
type Snapshot struct {
	Summary      Summary
	Pods         []PodRow
	Observations []Observation
}

// Scan runs the detectors over every cached pod and service.
func (c *Cluster) Scan(now time.Time) Snapshot {
	state, lastErr := c.Status()
	snap := Snapshot{Summary: Summary{Name: c.Name, State: state, Error: lastErr}}
	if !c.Synced() {
		return snap
	}

	nodes, _ := c.Nodes.List(labels.Everything())
	for _, n := range nodes {
		snap.Summary.Nodes++
		if nodeReady(n) {
			snap.Summary.NodesReady++
		}
		if n.Spec.Unschedulable {
			snap.Summary.Cordoned++
		}
	}

	pods, _ := c.Pods.List(labels.Everything())
	byKey := map[string]*Observation{}
	namespaces := map[string]bool{}
	for _, p := range pods {
		namespaces[p.Namespace] = true
		owner := c.Owner(p)
		findings := detect.Pod(p, c.EventsFor(string(p.UID)), now)

		row := PodRow{
			Cluster: c.Name, Namespace: p.Namespace, Name: p.Name,
			Status: PodStatus(p), Ready: podReady(p), Restarts: podRestarts(p),
			Created: p.CreationTimestamp.UTC().Format(time.RFC3339),
			Node:    p.Spec.NodeName, Owner: owner, Health: podHealth(p, len(findings) > 0),
		}
		for _, f := range findings {
			row.Families = append(row.Families, string(f.Family))
			key := Key(p.Namespace, owner, f.Family)
			if o, ok := byKey[key]; ok {
				o.Pods = append(o.Pods, p.Name)
			} else {
				byKey[key] = &Observation{Key: key, Namespace: p.Namespace, Workload: owner, Finding: f, Pods: []string{p.Name}}
			}
		}
		snap.Pods = append(snap.Pods, row)
		snap.Summary.Pods++
		switch row.Health {
		case "healthy", "done":
			snap.Summary.Healthy++
		case "failing":
			snap.Summary.Failing++
		default:
			snap.Summary.Pending++
		}
	}
	snap.Summary.Namespaces = len(namespaces)

	for _, o := range c.serviceObservations(pods) {
		o := o
		byKey[o.Key] = &o
	}

	for _, o := range byKey {
		sort.Strings(o.Pods)
		snap.Observations = append(snap.Observations, *o)
	}
	sort.Slice(snap.Observations, func(i, j int) bool { return snap.Observations[i].Key < snap.Observations[j].Key })
	sort.Slice(snap.Pods, func(i, j int) bool {
		a, b := snap.Pods[i], snap.Pods[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	return snap
}

// serviceObservations reports Services whose selector matches no pod
// while pods exist in the namespace. The pods look healthy in this
// failure, so it cannot be found from pod state alone.
func (c *Cluster) serviceObservations(pods []*corev1.Pod) []Observation {
	svcs, _ := c.Services.List(labels.Everything())
	byNS := map[string][]*corev1.Pod{}
	for _, p := range pods {
		if p.DeletionTimestamp == nil && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			byNS[p.Namespace] = append(byNS[p.Namespace], p)
		}
	}
	var out []Observation
	for _, s := range svcs {
		if len(s.Spec.Selector) == 0 || s.Spec.Type == corev1.ServiceTypeExternalName || len(byNS[s.Namespace]) == 0 {
			continue
		}
		sel := labels.SelectorFromSet(s.Spec.Selector)
		matched := false
		var near []string
		for _, p := range byNS[s.Namespace] {
			if sel.Matches(labels.Set(p.Labels)) {
				matched = true
				break
			}
			if nearMatch(s.Spec.Selector, p.Labels) {
				near = append(near, p.Name)
			}
		}
		// Report only when some pod's labels are a near miss for the
		// selector (same keys, values differing by punctuation or
		// case). A selector that matches nothing at all is usually a
		// Service whose workload is scaled to zero.
		if matched || len(near) == 0 {
			continue
		}
		sort.Strings(near)
		ref := Ref{Kind: "Service", Name: s.Name}
		out = append(out, Observation{
			Key: Key(s.Namespace, ref, detect.FamilyNetwork), Namespace: s.Namespace, Workload: ref, Pods: near,
			Finding: detect.Finding{
				Family: detect.FamilyNetwork, Reason: "NoEndpoints", Cause: detect.CauseSelector,
				Summary: "Service selector matches no pod, so the Service has no endpoints",
				Evidence: []detect.Evidence{
					{Label: "Service selector", Value: labels.Set(s.Spec.Selector).String()},
					{Label: "Pods whose labels nearly match", Value: strings.Join(near, ", ")},
				},
			},
		})
	}
	return out
}

// nearMatch reports whether the pod has every selector key and each
// value is equal once case and punctuation are ignored, with at least
// one value differing as written. "check-out" nearly matches "checkout".
func nearMatch(selector, podLabels map[string]string) bool {
	for k, want := range selector {
		got, ok := podLabels[k]
		if !ok || normalize(got) != normalize(want) {
			return false
		}
	}
	return true
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Key identifies an incident within a cluster.
func Key(namespace string, workload Ref, family detect.Family) string {
	return namespace + "/" + workload.Kind + "/" + workload.Name + "/" + string(family)
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podHealth(p *corev1.Pod, failing bool) string {
	switch {
	case failing:
		return "failing"
	case p.Status.Phase == corev1.PodSucceeded:
		return "done"
	case p.DeletionTimestamp != nil:
		return "pending"
	case p.Status.Phase == corev1.PodRunning:
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				return "pending"
			}
		}
		return "healthy"
	}
	return "pending"
}

func podReady(p *corev1.Pod) string {
	ready := 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(p.Spec.Containers))
}

func podRestarts(p *corev1.Pod) int32 {
	var n int32
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	for _, cs := range p.Status.InitContainerStatuses {
		n += cs.RestartCount
	}
	return n
}

// PodStatus is the short status string kubectl would print.
func PodStatus(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	if p.Status.Reason != "" {
		return p.Status.Reason
	}
	for _, cs := range p.Status.InitContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing" {
			return "Init:" + cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return "Init:" + orReason(cs.State.Terminated.Reason, "Error")
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" && cs.State.Terminated.Reason != "Completed" {
			return cs.State.Terminated.Reason
		}
	}
	return string(p.Status.Phase)
}

func orReason(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// DeploymentRolledOut reports whether a Deployment has finished rolling
// out its current spec.
func DeploymentRolledOut(d *appsv1.Deployment) bool {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	s := d.Status
	return s.ObservedGeneration >= d.Generation && s.UpdatedReplicas == want && s.Replicas == want && s.AvailableReplicas == want
}

// OOMEvents asks the node's kubelet (cAdvisor) how many out-of-memory
// events it has recorded for one container. It needs get on
// nodes/proxy, which is a powerful permission, so callers use it only
// when explicitly enabled. Some runtimes never report these events;
// a zero therefore means "not confirmed", not "did not happen".
func (c *Cluster) OOMEvents(ctx context.Context, node, namespace, pod, container string) (int, error) {
	raw, err := c.Client.CoreV1().RESTClient().Get().AbsPath("/api/v1/nodes/" + node + "/proxy/metrics/cadvisor").DoRaw(ctx)
	if err != nil {
		return 0, err
	}
	return ParseOOMEvents(string(raw), namespace, pod, container), nil
}

// ParseOOMEvents extracts container_oom_events_total for one container
// from cAdvisor's Prometheus text output.
func ParseOOMEvents(metrics, namespace, pod, container string) int {
	total := 0
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, "container_oom_events_total{") {
			continue
		}
		if !strings.Contains(line, `container="`+container+`"`) || !strings.Contains(line, `namespace="`+namespace+`"`) || !strings.Contains(line, `pod="`+pod+`"`) {
			continue
		}
		fields := strings.Fields(line[strings.LastIndex(line, "}")+1:])
		if len(fields) == 0 {
			continue
		}
		var n float64
		if _, err := fmt.Sscanf(fields[0], "%g", &n); err == nil {
			total += int(n)
		}
	}
	return total
}
