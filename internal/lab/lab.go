// SPDX-License-Identifier: Apache-2.0

// Package lab injects the demo failures from inside the console, so a
// demonstration does not need a terminal next to the browser.
//
// It is deliberately separate from the autofix write path. It is off
// unless `kbark serve` is started with a demo flag, it uses its own
// credentials (never the autofix service account, which cannot create
// anything), and every object it touches is in one namespace. kbark
// then detects what the lab broke exactly as it detects anything else.
package lab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	"github.com/shivangtanwar/kbark/demo"
)

// Namespace is the only namespace the lab writes to.
const Namespace = "kbark-demo"

const fieldManager = "kbark-demo"

// Reset is the pseudo-scenario that restores the healthy baseline.
const Reset = "reset"

// Scenario describes one injectable failure for the console.
type Scenario struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Breaks says what is done to the cluster.
	Breaks string `json:"breaks"`
	// Expect says what kbark should then propose.
	Expect string `json:"expect"`
	// Risk is the risk of the fix kbark proposes: low, medium, or ""
	// when there is no automatic fix. Policy may block medium.
	Risk string `json:"risk"`
	// Note is a precondition or follow-up worth knowing.
	Note string `json:"note,omitempty"`
	// Workload is the name of the object the incident will be about,
	// so the console can link the scenario to its incident.
	Workload string `json:"workload"`
	// Group orders the list: failures first, helpers last.
	Group string `json:"group"`

	run func(ctx context.Context, t *target, step func(string)) error
}

// Scenarios is the catalogue, in display order.
func Scenarios() []Scenario { return scenarios }

var scenarios = []Scenario{
	{
		ID: "oom", Workload: "mem-hog", Title: "Out of memory", Group: "Resources", Risk: "low",
		Breaks: "Deploys mem-hog, which loads about 90 MiB under a 64Mi memory limit.",
		Expect: "Raise the memory limit to 128Mi.",
		run:    apply("oom.yaml"),
	},
	{
		ID: "disk", Workload: "uploader", Title: "Local disk limit exceeded", Group: "Resources", Risk: "low",
		Breaks: "Deploys uploader, which writes 24 MiB to local disk under a 16Mi limit; the node evicts each pod.",
		Expect: "Raise the ephemeral-storage limit to 32Mi, then a second proposal deletes the dead pod records.",
		Note:   "A dead pod is added about every 30 seconds until the fix is approved.",
		run:    apply("disk.yaml"),
	},
	{
		ID: "crash", Workload: "worker", Title: "Crashing rollout", Group: "Rollouts", Risk: "medium",
		Breaks: "Rolls worker from a healthy revision 1 to a revision 2 that exits on start.",
		Expect: "Roll the Deployment back to revision 1.",
		run:    rollout("Deployment", "worker", "worker-v1.yaml", "worker-v2-crash.yaml"),
	},
	{
		ID: "bad-image", Workload: "api", Title: "Image that does not exist", Group: "Rollouts", Risk: "medium",
		Breaks: "Rolls api from a healthy revision 1 to a revision 2 with a tag that was never pushed.",
		Expect: "Roll the Deployment back to revision 1.",
		run:    rollout("Deployment", "api", "api-v1.yaml", "api-v2-bad-image.yaml"),
	},
	{
		ID: "no-account", Workload: "gateway", Title: "Pods refused at creation", Group: "Rollouts", Risk: "medium",
		Breaks: "Rolls gateway to a revision that names a ServiceAccount nobody created. No pod of it is ever created.",
		Expect: "Roll the Deployment back to revision 1.",
		run:    rollout("Deployment", "gateway", "gateway-v1.yaml", "gateway-v2.yaml"),
	},
	{
		ID: "sts-crash", Workload: "ledger", Title: "Crashing StatefulSet rollout", Group: "Rollouts", Risk: "medium",
		Breaks: "Rolls the ledger StatefulSet to a revision whose migration fails.",
		Expect: "Roll back to revision 1 and replace the broken pod.",
		run:    rollout("StatefulSet", "ledger", "ledger-v1.yaml", "ledger-v2.yaml"),
	},
	{
		ID: "slow-start", Workload: "slow-start", Title: "Liveness probe kills a slow starter", Group: "Probes", Risk: "low",
		Breaks: "Deploys an application that needs longer to start than its liveness probe allows.",
		Expect: "Relax the probe's initial delay and timeout.",
		Note:   "Verification waits out the new initial delay, about a minute.",
		run:    apply("slow-start.yaml"),
	},
	{
		ID: "slow-ready", Workload: "search", Title: "Readiness check too slow", Group: "Probes", Risk: "low",
		Breaks: "Deploys search, whose readiness check takes two seconds and is given one.",
		Expect: "Raise the readiness timeout to 5 seconds.",
		Note:   "Reported after the pod has been unready for a minute.",
		run:    apply("slow-ready.yaml"),
	},
	{
		ID: "never-pull", Workload: "thumbnailer", Title: "Image may not be pulled", Group: "Configuration", Risk: "low",
		Breaks: "Deploys thumbnailer with an image that is not on the node and the pull policy Never.",
		Expect: "Change the pull policy to IfNotPresent.",
		run:    apply("never-pull.yaml"),
	},
	{
		ID: "selector", Workload: "checkout", Title: "Service selector typo", Group: "Configuration", Risk: "medium",
		Breaks: "Deploys checkout with a Service whose selector says check-out; the Service has no endpoints.",
		Expect: "Correct the Service selector.",
		run:    apply("selector-mismatch.yaml"),
	},
	{
		ID: "missing-config", Workload: "reports", Title: "Missing ConfigMap", Group: "Configuration", Risk: "",
		Breaks: "Deploys reports, which reads a ConfigMap that does not exist.",
		Expect: "No automatic fix: kbark names the missing ConfigMap and stops.",
		Note:   "Then run \"Create the missing ConfigMap\" and the incident closes by itself.",
		run:    apply("missing-config.yaml"),
	},
	{
		ID: "stuck-pod", Workload: "sessions", Title: "One pod with corrupt local state", Group: "Pods and nodes", Risk: "low",
		Breaks: "Marks the local state of one of the three sessions pods corrupt; within about a minute it crash-loops.",
		Expect: "Replace that one pod; the other two prove the revision is sound.",
		Note:   "Needs the sessions pods to have been ready for five minutes, so not right after a reset.",
		run:    corruptOnePod,
	},
	{
		ID: "evicted", Workload: "cache-7c9d4-evicted", Title: "Evicted pod left behind", Group: "Pods and nodes", Risk: "low",
		Breaks: "Creates the dead record of a pod evicted for node memory pressure.",
		Expect: "Delete the dead pod record.",
		run:    evictedRecord,
	},
	{
		ID: "cordon", Workload: "batch", Title: "Pods pending on a cordoned node", Group: "Pods and nodes", Risk: "medium",
		Breaks: "Cordons every node, then deploys batch, whose pods cannot be scheduled.",
		Expect: "Uncordon the node.",
		Note:   "Nothing else can be scheduled on this cluster until the fix is approved or the namespace is reset.",
		run:    cordonAndDeploy,
	},
	{
		ID: "fix-config", Workload: "reports", Title: "Create the missing ConfigMap", Group: "Helpers", Risk: "",
		Breaks: "Creates the ConfigMap that the \"Missing ConfigMap\" scenario lacks, as an operator would.",
		Expect: "The open incident clears on its own.",
		run:    apply("reports-config.yaml"),
	},
}

func find(id string) (Scenario, bool) {
	for _, s := range scenarios {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}

// Status is what the lab last did on one cluster.
type Status struct {
	Cluster string `json:"cluster"`
	// Running lists the scenarios in progress. Different scenarios
	// may run side by side; a reset runs alone.
	Running []Step `json:"running"`
	// Last is the most recently finished scenario and how it ended.
	Last       string     `json:"last,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Actor      string     `json:"actor,omitempty"`
}

// Step is one scenario in progress and what it is doing.
type Step struct {
	ID   string `json:"id"`
	Step string `json:"step"`
}

type target struct {
	name string
	kube kubernetes.Interface
	dyn  dynamic.Interface
}

// Lab runs scenarios, one at a time per cluster.
type Lab struct {
	order   []string
	targets map[string]*target
	// OnChange is called when a status changes, so the console can refresh.
	OnChange func()
	// Log receives one line per started and finished scenario.
	Log func(cluster, scenario, actor, note string)

	mu     sync.Mutex
	status map[string]*Status
	wg     sync.WaitGroup
}

// New builds a lab over the given cluster configurations.
func New(order []string, cfgs map[string]*rest.Config) (*Lab, error) {
	l := &Lab{order: order, targets: map[string]*target{}, status: map[string]*Status{}}
	for _, name := range order {
		cfg := cfgs[name]
		if cfg == nil {
			return nil, fmt.Errorf("demo lab: no credentials for cluster %q", name)
		}
		kube, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("demo lab: %s: %w", name, err)
		}
		dyn, err := dynamic.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("demo lab: %s: %w", name, err)
		}
		l.targets[name] = &target{name: name, kube: kube, dyn: dyn}
		l.status[name] = &Status{Cluster: name}
	}
	return l, nil
}

// Statuses returns the per-cluster status in configured order.
func (l *Lab) Statuses() []Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Status, 0, len(l.order))
	for _, name := range l.order {
		st := *l.status[name]
		st.Running = append([]Step{}, st.Running...)
		out = append(out, st)
	}
	return out
}

// Errors the HTTP layer maps to status codes.
var (
	ErrUnknown = errors.New("unknown cluster or scenario")
	ErrBusy    = errors.New("that step is already running, or a reset is in progress on this cluster")
)

// Start runs a scenario (or Reset) on a cluster in the background.
func (l *Lab) Start(cluster, id, actor string) error {
	t := l.targets[cluster]
	if t == nil {
		return ErrUnknown
	}
	run := resetNamespace
	if id != Reset {
		s, ok := find(id)
		if !ok {
			return ErrUnknown
		}
		run = s.run
	}

	l.mu.Lock()
	st := l.status[cluster]
	for _, r := range st.Running {
		if r.ID == id || r.ID == Reset || id == Reset {
			l.mu.Unlock()
			return ErrBusy
		}
	}
	st.Running, st.Actor = append(st.Running, Step{ID: id, Step: "starting"}), actor
	l.mu.Unlock()
	l.changed()
	l.log(cluster, id, actor, "started")

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		err := run(ctx, t, func(step string) {
			l.mu.Lock()
			for i := range st.Running {
				if st.Running[i].ID == id {
					st.Running[i].Step = step
				}
			}
			l.mu.Unlock()
			l.changed()
		})
		now := time.Now()
		l.mu.Lock()
		rest := []Step{}
		for _, r := range st.Running {
			if r.ID != id {
				rest = append(rest, r)
			}
		}
		st.Running, st.Last, st.FinishedAt, st.LastError = rest, id, &now, ""
		if err != nil {
			st.LastError = err.Error()
		}
		l.mu.Unlock()
		l.changed()
		if err != nil {
			l.log(cluster, id, actor, "failed: "+err.Error())
		} else {
			l.log(cluster, id, actor, "done")
		}
	}()
	return nil
}

// Wait blocks until running scenarios finish. For tests.
func (l *Lab) Wait() { l.wg.Wait() }

func (l *Lab) changed() {
	if l.OnChange != nil {
		l.OnChange()
	}
}

func (l *Lab) log(cluster, scenario, actor, note string) {
	if l.Log != nil {
		l.Log(cluster, scenario, actor, note)
	}
}

// --- steps ---

var resources = map[string]schema.GroupVersionResource{
	"Deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"Service":     {Version: "v1", Resource: "services"},
	"ConfigMap":   {Version: "v1", Resource: "configmaps"},
	"Pod":         {Version: "v1", Resource: "pods"},
}

// applyFile server-side-applies every object in a fixture. Objects of
// an unexpected kind or namespace are refused rather than applied.
func applyFile(ctx context.Context, t *target, file string) error {
	raw, err := demo.Fixtures.ReadFile("fixtures/" + file)
	if err != nil {
		return err
	}
	for _, doc := range bytes.Split(raw, []byte("\n---")) {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		js, err := yaml.YAMLToJSON(doc)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		var obj unstructured.Unstructured
		if err := obj.UnmarshalJSON(js); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		gvr, ok := resources[obj.GetKind()]
		if !ok || obj.GetNamespace() != Namespace {
			return fmt.Errorf("%s: the lab does not apply %s in namespace %q", file, obj.GetKind(), obj.GetNamespace())
		}
		force := true
		if _, err := t.dyn.Resource(gvr).Namespace(Namespace).Patch(ctx, obj.GetName(), types.ApplyPatchType, js,
			metav1.PatchOptions{FieldManager: fieldManager, Force: &force}); err != nil {
			return fmt.Errorf("apply %s/%s: %w", strings.ToLower(obj.GetKind()), obj.GetName(), err)
		}
	}
	return nil
}

func apply(file string) func(context.Context, *target, func(string)) error {
	return func(ctx context.Context, t *target, step func(string)) error {
		step("applying " + file)
		return applyFile(ctx, t, file)
	}
}

// rollout applies a healthy first revision, waits for it to be up, and
// then applies the broken second revision, so a rollback has a target.
func rollout(kind, name, first, second string) func(context.Context, *target, func(string)) error {
	return func(ctx context.Context, t *target, step func(string)) error {
		step("applying the healthy revision")
		if err := applyFile(ctx, t, first); err != nil {
			return err
		}
		step("waiting for the healthy revision to be ready")
		if err := waitReady(ctx, t, kind, name, 100*time.Second); err != nil {
			return err
		}
		step("applying the broken revision")
		return applyFile(ctx, t, second)
	}
}

func waitReady(ctx context.Context, t *target, kind, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, err := isReady(ctx, t, kind, name)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s/%s was not ready after %s", strings.ToLower(kind), name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func isReady(ctx context.Context, t *target, kind, name string) (bool, error) {
	want := func(p *int32) int32 {
		if p == nil {
			return 1
		}
		return *p
	}
	switch kind {
	case "Deployment":
		d, err := t.kube.AppsV1().Deployments(Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		n := want(d.Spec.Replicas)
		return d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas == n && d.Status.Replicas == n && d.Status.AvailableReplicas == n, nil
	case "StatefulSet":
		s, err := t.kube.AppsV1().StatefulSets(Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		n := want(s.Spec.Replicas)
		return s.Status.ObservedGeneration >= s.Generation && s.Status.UpdatedReplicas == n && s.Status.ReadyReplicas == n, nil
	}
	return false, fmt.Errorf("cannot wait for a %s", kind)
}

func evictedRecord(ctx context.Context, t *target, step func(string)) error {
	step("creating the pod record")
	if err := applyFile(ctx, t, "evicted-pod.yaml"); err != nil {
		return err
	}
	step("marking it evicted, as the kubelet would")
	patch := `{"status":{"phase":"Failed","reason":"Evicted","message":"The node was low on resource: memory. Threshold quantity: 100Mi, available: 62Mi."}}`
	_, err := t.kube.CoreV1().Pods(Namespace).Patch(ctx, "cache-7c9d4-evicted", types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: fieldManager}, "status")
	return err
}

func setCordon(ctx context.Context, t *target, value bool) error {
	nodes, err := t.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, value)
	for _, n := range nodes.Items {
		if n.Spec.Unschedulable == value {
			continue
		}
		if _, err := t.kube.CoreV1().Nodes().Patch(ctx, n.Name, types.StrategicMergePatchType, []byte(body), metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
			return err
		}
	}
	return nil
}

func cordonAndDeploy(ctx context.Context, t *target, step func(string)) error {
	step("cordoning the nodes")
	if err := setCordon(ctx, t, true); err != nil {
		return err
	}
	step("applying batch.yaml")
	return applyFile(ctx, t, "batch.yaml")
}

func corruptOnePod(ctx context.Context, t *target, step func(string)) error {
	step("choosing a sessions pod")
	pods, err := t.kube.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=sessions"})
	if err != nil {
		return err
	}
	var names []string
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil && len(p.Status.ContainerStatuses) > 0 && p.Status.ContainerStatuses[0].Ready {
			names = append(names, p.Name)
		}
	}
	if len(names) < 2 {
		return errors.New("fewer than two sessions pods are running; reset the namespace and wait for them")
	}
	sort.Strings(names)
	step("marking the state of " + names[0] + " corrupt")
	// The container reads this annotation through a downward-API file,
	// so nothing is executed inside the pod. The kubelet refreshes that
	// file within about a minute.
	patch := `{"metadata":{"annotations":{"kbark.dev/demo-state":"corrupt"}}}`
	_, err = t.kube.CoreV1().Pods(Namespace).Patch(ctx, names[0], types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: fieldManager})
	return err
}

// resetNamespace removes everything the scenarios created and restores
// the healthy baseline. It never deletes the namespace itself, so the
// lab needs no cluster-wide delete permission.
func resetNamespace(ctx context.Context, t *target, step func(string)) error {
	step("uncordoning nodes")
	if err := setCordon(ctx, t, false); err != nil {
		return err
	}
	step("removing demo workloads")
	zero := int64(0)
	for _, kind := range []string{"Deployment", "StatefulSet", "Service", "ConfigMap", "Pod"} {
		res := t.dyn.Resource(resources[kind]).Namespace(Namespace)
		list, err := res.List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list %ss: %w", strings.ToLower(kind), err)
		}
		for _, o := range list.Items {
			if kind == "ConfigMap" && o.GetName() == "kube-root-ca.crt" {
				continue
			}
			opts := metav1.DeleteOptions{}
			if kind == "Pod" {
				opts.GracePeriodSeconds = &zero
			}
			if err := res.Delete(ctx, o.GetName(), opts); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete %s/%s: %w", strings.ToLower(kind), o.GetName(), err)
			}
		}
	}
	step("waiting for the old pods to go")
	deadline := time.Now().Add(90 * time.Second)
	for {
		pods, err := t.kube.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		if len(pods.Items) == 0 || time.Now().After(deadline) {
			break
		}
		// Pods recreated by a controller that was still terminating.
		for _, p := range pods.Items {
			if p.DeletionTimestamp == nil {
				_ = t.kube.CoreV1().Pods(Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	step("applying the healthy baseline")
	return applyFile(ctx, t, "healthy.yaml")
}
