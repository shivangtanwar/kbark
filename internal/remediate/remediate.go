// SPDX-License-Identifier: Apache-2.0

// Package remediate is kbark's entire write path. It turns a detected
// failure into one typed action (Plan), performs an approved action
// (Apply), reverses it (Rollback), and reports whether the cluster has
// finished reacting (Settled).
//
// The set of actions is closed: the five constants in package incident.
// Nothing in this package executes text produced by a language model.
package remediate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/shivangtanwar/kbark/internal/detect"
	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
)

const revisionAnnotation = "deployment.kubernetes.io/revision"

// Planner chooses the fix for an incident.
type Planner struct {
	// DryRun validates each proposal against the API server with
	// dryRun=All before it is shown. Off only in unit tests.
	DryRun bool
	// MemoryCeiling is the largest memory limit kbark will propose.
	MemoryCeiling resource.Quantity
	// ConfirmOOM asks the node's kubelet whether it recorded an
	// out-of-memory event before proposing a fix for an inferred one.
	// Needs get on nodes/proxy; off by default.
	ConfirmOOM bool
}

// Plan is the outcome of planning: exactly one of Action or Manual is set.
type Plan struct {
	Action *incident.Action
	// Manual explains why kbark will not propose a change.
	Manual string
}

func manual(format string, args ...any) Plan { return Plan{Manual: fmt.Sprintf(format, args...)} }

// Plan picks the playbook for the incident's failure family.
func (p *Planner) Plan(ctx context.Context, c *fleet.Cluster, inc *incident.Incident) Plan {
	switch inc.Family {
	case detect.FamilyEvicted:
		return p.planDeletePods(c, inc)
	case detect.FamilyStorage:
		return manual("Storage changes can lose data, so kbark does not make them. Check the PersistentVolumeClaim, its StorageClass and the volume events shown in the evidence.")
	case detect.FamilyNetwork:
		return manual("kbark cannot tell whether the Service selector or the pod labels are the side that is wrong. Align them so the selector matches the intended pods.")
	case detect.FamilyScheduling:
		if inc.Cause == detect.CauseCordoned {
			return p.planUncordon(ctx, c)
		}
	}

	if inc.Workload.Kind == "Pod" {
		return manual("This pod is not managed by a controller, and most of a running pod's spec cannot be changed in place. Recreate it with a corrected spec.")
	}
	if !editable(inc.Workload.Kind) {
		return manual("kbark does not edit %s objects.", inc.Workload.Kind)
	}

	switch inc.Family {
	case detect.FamilyOOM:
		return p.planMemory(ctx, c, inc)

	case detect.FamilyImagePull:
		if plan, ok := p.planRollback(ctx, c, inc, true); ok {
			return plan
		}
		switch inc.Cause {
		case detect.CauseAuth:
			return manual("The registry refused the pull. This needs a valid imagePullSecret, which kbark cannot create.")
		case detect.CauseRegistry:
			return manual("The registry could not be reached. This is a network or registry availability problem, not something a change to the workload fixes.")
		}
		return manual("The image reference does not resolve and there is no earlier revision with a different image to return to. Correct the image name or tag.")

	case detect.FamilyCrash:
		if plan, ok := p.planRestartPods(c, inc); ok {
			return plan
		}
		if plan, ok := p.planRollback(ctx, c, inc, false); ok {
			return plan
		}
		return manual("The container exits on its own and there is no earlier revision to return to. Restarting it would change nothing; the cause is in the application, its arguments or a dependency. See the explanation and the previous container log.")

	case detect.FamilyConfig:
		if plan, ok := p.planRollback(ctx, c, inc, false); ok {
			return plan
		}
		return manual("A ConfigMap, Secret or key the pod references does not exist, and there is no earlier revision without that reference. kbark does not create configuration because it cannot know the right values. Create the missing object named in the evidence.")

	case detect.FamilyProbe:
		if plan, ok := p.planRestartPods(c, inc); ok {
			return plan
		}
		tunable := inc.Probe != "readiness" && (inc.Cause == detect.CauseProbeTimeout || inc.Cause == detect.CauseProbeRefused)
		if plan, ok := p.planRollback(ctx, c, inc, false); ok && (!tunable || probeChanged(plan.Action, inc)) {
			return plan
		}
		if tunable {
			return p.planProbe(ctx, c, inc)
		}
		return manual("The application itself answers the probe with a failure, and there is no earlier revision to return to. Changing probe timing would only hide that.")

	case detect.FamilyScheduling:
		if plan, ok := p.planRollback(ctx, c, inc, false); ok {
			return plan
		}
		if inc.Cause == detect.CauseInsufficient {
			return manual("No node has enough free capacity for this pod's requests. Adding nodes or lowering requests is a capacity decision kbark does not make.")
		}
		return manual("No node satisfies this pod's placement rules (selector, affinity or taints) and there is no earlier revision to return to. Correct the rules or label the intended nodes.")
	}
	return manual("kbark has no playbook for this failure.")
}

// --- set-memory-limit ---

func (p *Planner) planMemory(ctx context.Context, c *fleet.Cluster, inc *incident.Incident) Plan {
	w, err := getWorkload(ctx, c.Client, inc.Namespace, inc.Workload.Kind, inc.Workload.Name)
	if err != nil {
		return manual("Could not read %s: %v", inc.Workload, err)
	}
	ctr := findContainer(w.template, inc.Container, inc.Init)
	if ctr == nil {
		return manual("Container %q is no longer part of %s.", inc.Container, inc.Workload)
	}
	old, ok := ctr.Resources.Limits[corev1.ResourceMemory]
	if !ok || old.IsZero() {
		return manual("Container %q has no memory limit, so it was the node that ran out of memory. Set requests and limits for the workloads on this node.", inc.Container)
	}
	if old.Cmp(p.MemoryCeiling) >= 0 {
		return manual("The memory limit (%s) is already at kbark's ceiling (%s). Repeated OOM kills at this size suggest a leak rather than a limit that is too low.", old.String(), p.MemoryCeiling.String())
	}
	next := old.DeepCopy()
	next.Add(old)
	if next.Cmp(p.MemoryCeiling) > 0 {
		next = p.MemoryCeiling.DeepCopy()
	}

	a := &incident.Action{
		Type:       incident.ActionSetMemoryLimit,
		TargetKind: inc.Workload.Kind, TargetName: inc.Workload.Name,
		Params: map[string]string{
			"container": inc.Container, "init": strconv.FormatBool(inc.Init),
			"from": old.String(), "to": next.String(),
		},
		Title:      fmt.Sprintf("Raise memory limit of container %q from %s to %s", inc.Container, old.String(), next.String()),
		Rationale:  fmt.Sprintf("The container was killed by the kernel for exceeding its %s memory limit. Doubling the limit (capped at %s) gives it room to run; if it is killed again at the new limit, the cause is more likely a leak.", old.String(), p.MemoryCeiling.String()),
		Risk:       "low",
		Reversible: true,
	}
	confirmed := 0
	if inc.Cause == detect.CauseInferred && p.ConfirmOOM {
		confirmed = p.oomEvents(ctx, c, inc)
	}
	if confirmed > 0 {
		a.Params["confirmed"] = strconv.Itoa(confirmed)
		a.Rationale = fmt.Sprintf("The container runtime reported these kills as a plain exit 137, but the node's kubelet recorded %d out-of-memory event(s) for this container, which confirms the cause. Doubling the %s limit (capped at %s) gives it room to run.", confirmed, old.String(), p.MemoryCeiling.String())
	} else if inc.Cause == detect.CauseInferred {
		a.Rationale = fmt.Sprintf("The container is repeatedly killed with SIGKILL (exit 137) under a %s memory limit, with no probe kill to explain it. That pattern is an out-of-memory kill. Doubling the limit (capped at %s) gives it room to run.", old.String(), p.MemoryCeiling.String())
		a.Warnings = append(a.Warnings, "The container runtime did not label these kills as OOMKilled, so the out-of-memory cause is inferred, not confirmed. If memory is not the cause, verification will fail and the change can be rolled back.")
	}
	return p.finish(ctx, c, inc, a)
}

// --- tune-probe ---

func (p *Planner) planProbe(ctx context.Context, c *fleet.Cluster, inc *incident.Incident) Plan {
	w, err := getWorkload(ctx, c.Client, inc.Namespace, inc.Workload.Kind, inc.Workload.Name)
	if err != nil {
		return manual("Could not read %s: %v", inc.Workload, err)
	}
	ctr := findContainer(w.template, inc.Container, false)
	if ctr == nil {
		return manual("Container %q is no longer part of %s.", inc.Container, inc.Workload)
	}
	probe := probeOf(ctr, inc.Probe)
	if probe == nil {
		return manual("Container %q no longer has a %s probe.", inc.Container, inc.Probe)
	}
	delay := max(probe.InitialDelaySeconds*2, 30)
	timeout := max(probe.TimeoutSeconds, 5)
	if probe.InitialDelaySeconds >= 300 {
		return manual("The %s probe already waits %ds before its first check. A container that is not up by then is not merely slow to start.", inc.Probe, probe.InitialDelaySeconds)
	}
	delay = min(delay, 300)

	a := &incident.Action{
		Type:       incident.ActionTuneProbe,
		TargetKind: inc.Workload.Kind, TargetName: inc.Workload.Name,
		Params: map[string]string{
			"container": inc.Container, "probe": inc.Probe,
			"initialDelaySeconds": strconv.Itoa(int(delay)), "timeoutSeconds": strconv.Itoa(int(timeout)),
		},
		Title: fmt.Sprintf("Give container %q more time before its %s probe: initial delay %ds to %ds, timeout %ds to %ds",
			inc.Container, inc.Probe, probe.InitialDelaySeconds, delay, max(probe.TimeoutSeconds, 1), timeout),
		Rationale:  fmt.Sprintf("The kubelet keeps killing the container because the %s probe fails before the application is listening. The probe target is unchanged; only its timing is relaxed.", inc.Probe),
		Risk:       "low",
		Reversible: true,
	}
	return p.finish(ctx, c, inc, a)
}

func probeOf(c *corev1.Container, kind string) *corev1.Probe {
	switch kind {
	case "liveness":
		return c.LivenessProbe
	case "startup":
		return c.StartupProbe
	}
	return nil
}

// --- rollback-workload ---

// revisionPair is the latest and previous revision of a workload.
type revisionPair struct {
	cur, prev         int64
	curTmpl, prevTmpl *corev1.PodTemplateSpec
	// prevReady is how many pods of the previous revision are still
	// ready, when that is knowable (Deployments only).
	prevReady int32
	// onCurrent reports whether a pod belongs to the latest revision.
	onCurrent func(*corev1.Pod) bool
}

// revisions finds the two most recent revisions of the incident's
// workload: ReplicaSets for a Deployment, ControllerRevisions for a
// StatefulSet or DaemonSet.
func revisions(c *fleet.Cluster, inc *incident.Incident) (*revisionPair, metav1.Object, bool) {
	ns, name := inc.Namespace, inc.Workload.Name
	switch inc.Workload.Kind {
	case "Deployment":
		d, err := c.Deployments.Deployments(ns).Get(name)
		if err != nil {
			return nil, nil, false
		}
		all, _ := c.ReplicaSets.ReplicaSets(ns).List(labels.Everything())
		var rss []*appsv1.ReplicaSet
		for _, rs := range all {
			if metav1.IsControlledBy(rs, d) && revision(rs) > 0 {
				rss = append(rss, rs)
			}
		}
		if len(rss) < 2 {
			return nil, nil, false
		}
		sort.Slice(rss, func(i, j int) bool { return revision(rss[i]) > revision(rss[j]) })
		cur, prev := rss[0], rss[1]
		tmpl := prev.Spec.Template.DeepCopy()
		delete(tmpl.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
		return &revisionPair{
			cur: revision(cur), prev: revision(prev), curTmpl: &cur.Spec.Template, prevTmpl: tmpl,
			prevReady: prev.Status.ReadyReplicas,
			onCurrent: func(p *corev1.Pod) bool { return metav1.IsControlledBy(p, cur) },
		}, d, true

	case "StatefulSet", "DaemonSet":
		var owner metav1.Object
		if inc.Workload.Kind == "StatefulSet" {
			o, err := c.StatefulSets.StatefulSets(ns).Get(name)
			if err != nil {
				return nil, nil, false
			}
			owner = o
		} else {
			o, err := c.DaemonSets.DaemonSets(ns).Get(name)
			if err != nil {
				return nil, nil, false
			}
			owner = o
		}
		all, _ := c.Revisions.ControllerRevisions(ns).List(labels.Everything())
		var crs []*appsv1.ControllerRevision
		for _, cr := range all {
			if metav1.IsControlledBy(cr, owner) {
				crs = append(crs, cr)
			}
		}
		if len(crs) < 2 {
			return nil, nil, false
		}
		sort.Slice(crs, func(i, j int) bool { return crs[i].Revision > crs[j].Revision })
		cur, prev := crs[0], crs[1]
		curTmpl, err1 := revisionTemplate(cur)
		prevTmpl, err2 := revisionTemplate(prev)
		if err1 != nil || err2 != nil {
			return nil, nil, false
		}
		hash := cur.Name
		if i := strings.LastIndex(hash, "-"); inc.Workload.Kind == "DaemonSet" && i >= 0 {
			// DaemonSet pods carry only the hash suffix of the revision name.
			hash = hash[i+1:]
		}
		return &revisionPair{
			cur: cur.Revision, prev: prev.Revision, curTmpl: curTmpl, prevTmpl: prevTmpl,
			onCurrent: func(p *corev1.Pod) bool {
				return metav1.IsControlledBy(p, owner) && p.Labels[appsv1.ControllerRevisionHashLabelKey] == hash
			},
		}, owner, true
	}
	return nil, nil, false
}

// revisionTemplate extracts the pod template a ControllerRevision
// stores. The data is a patch of the form {"spec":{"template":{...}}}.
func revisionTemplate(cr *appsv1.ControllerRevision) (*corev1.PodTemplateSpec, error) {
	var patch struct {
		Spec struct {
			Template map[string]any `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(cr.Data.Raw, &patch); err != nil {
		return nil, err
	}
	if patch.Spec.Template == nil {
		return nil, errors.New("revision has no pod template")
	}
	delete(patch.Spec.Template, "$patch")
	raw, err := json.Marshal(patch.Spec.Template)
	if err != nil {
		return nil, err
	}
	var tmpl corev1.PodTemplateSpec
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		return nil, err
	}
	return &tmpl, nil
}

// planRollback proposes returning a workload to its previous revision.
// It applies only when the failing pods belong to the latest revision
// and an earlier one exists. needImageChange additionally requires the
// two revisions to differ in the failing container's image (a rollback
// cannot fix a pull failure otherwise).
func (p *Planner) planRollback(ctx context.Context, c *fleet.Cluster, inc *incident.Incident, needImageChange bool) (Plan, bool) {
	rev, owner, ok := revisions(c, inc)
	if !ok {
		return Plan{}, false
	}
	onCurrent := false
	for _, name := range inc.Pods {
		if pod, err := c.Pods.Pods(inc.Namespace).Get(name); err == nil && rev.onCurrent(pod) {
			onCurrent = true
		}
	}
	if !onCurrent {
		return Plan{}, false
	}
	a, b := findContainer(rev.curTmpl, inc.Container, inc.Init), findContainer(rev.prevTmpl, inc.Container, inc.Init)
	if needImageChange && (a == nil || b == nil || a.Image == b.Image) {
		return Plan{}, false
	}
	probeTouched := false
	if a != nil && b != nil && !inc.Init {
		probeTouched = !apiequality.Semantic.DeepEqual(a.LivenessProbe, b.LivenessProbe) ||
			!apiequality.Semantic.DeepEqual(a.StartupProbe, b.StartupProbe) ||
			!apiequality.Semantic.DeepEqual(a.ReadinessProbe, b.ReadinessProbe) ||
			!apiequality.Semantic.DeepEqual(a.Ports, b.Ports)
	}
	raw, err := json.Marshal(rev.prevTmpl)
	if err != nil {
		return Plan{}, false
	}

	serving := ""
	if rev.prevReady > 0 {
		serving = fmt.Sprintf(" Revision %d still has %d ready pod(s) serving.", rev.prev, rev.prevReady)
	}
	kind := inc.Workload.Kind
	act := &incident.Action{
		Type:       incident.ActionRollbackWorkload,
		TargetKind: kind, TargetName: owner.GetName(),
		Params: map[string]string{
			"fromRevision": strconv.FormatInt(rev.cur, 10),
			"toRevision":   strconv.FormatInt(rev.prev, 10),
			"probeChanged": strconv.FormatBool(probeTouched),
		},
		Title: fmt.Sprintf("Roll %s %q back from revision %d to revision %d", kind, owner.GetName(), rev.cur, rev.prev),
		Rationale: fmt.Sprintf("The failing pods all belong to revision %d, the latest rollout.%s Returning to the revision %d pod template restores the last configuration before this failure without kbark needing to guess a forward fix.",
			rev.cur, serving, rev.prev),
		Risk:       "medium",
		Reversible: true,
		Template:   raw,
	}
	plan := p.finish(ctx, c, inc, act)
	return plan, plan.Action != nil
}

func revision(rs *appsv1.ReplicaSet) int64 {
	n, _ := strconv.ParseInt(rs.Annotations[revisionAnnotation], 10, 64)
	return n
}

// siblingSettle is how long a sibling pod must have been ready before
// it counts as proof that the revision itself is sound.
const siblingSettle = 5 * time.Minute

// planRestartPods proposes replacing individual unhealthy pods. It
// applies only when other pods of the same workload and the same
// revision have been ready for a while: that shows the spec is fine
// and the problem is local to the failing pods (a stuck process, a bad
// node-local state), which a fresh pod clears. If every pod of the
// revision fails, this returns false and rollback is considered.
func (p *Planner) planRestartPods(c *fleet.Cluster, inc *incident.Incident) (Plan, bool) {
	if inc.Init || len(inc.Pods) == 0 {
		return Plan{}, false
	}
	failing := map[string]*corev1.Pod{}
	for _, name := range inc.Pods {
		if pod, err := c.Pods.Pods(inc.Namespace).Get(name); err == nil {
			failing[name] = pod
		}
	}
	if len(failing) == 0 {
		return Plan{}, false
	}
	sameRevision := func(a, b *corev1.Pod) bool {
		for _, key := range []string{appsv1.DefaultDeploymentUniqueLabelKey, appsv1.ControllerRevisionHashLabelKey} {
			if a.Labels[key] != b.Labels[key] {
				return false
			}
		}
		return true
	}
	var first *corev1.Pod
	for _, pod := range failing {
		first = pod
		break
	}
	all, _ := c.Pods.Pods(inc.Namespace).List(labels.Everything())
	healthy := 0
	for _, pod := range all {
		if _, bad := failing[pod.Name]; bad || pod.DeletionTimestamp != nil || c.Owner(pod) != inc.Workload || !sameRevision(pod, first) {
			continue
		}
		if readySince(pod) >= siblingSettle {
			healthy++
		}
	}
	if healthy == 0 {
		return Plan{}, false
	}

	var refs, lines, names []string
	for name, pod := range failing {
		names = append(names, name)
		refs = append(refs, name+"="+string(pod.UID))
	}
	sort.Strings(names)
	sort.Strings(refs)
	for _, name := range names {
		lines = append(lines, fmt.Sprintf("-pod/%s  (%s)", name, fleet.PodStatus(failing[name])))
	}
	lines = append(lines, fmt.Sprintf("+%d replacement pod(s) created by %s", len(names), strings.ToLower(inc.Workload.String())))
	title := fmt.Sprintf("Replace unhealthy pod %q", names[0])
	if len(names) > 1 {
		title = fmt.Sprintf("Replace %d unhealthy pods of %s", len(names), inc.Workload)
	}
	return Plan{Action: &incident.Action{
		Type:       incident.ActionRestartPod,
		TargetKind: "Pod", TargetName: names[0],
		Params: map[string]string{"pods": strings.Join(refs, ","), "healthySiblings": strconv.Itoa(healthy)},
		Title:  title,
		Rationale: fmt.Sprintf("%d other pod(s) of the same workload and the same revision have been ready for over %s, so the spec itself is sound and the problem is local to the failing pod(s). Deleting them lets the controller start fresh replacements.",
			healthy, siblingSettle),
		Risk:       "low",
		Diff:       fmt.Sprintf("--- namespace/%s pods (current)\n+++ namespace/%s pods (proposed)\n%s\n", inc.Namespace, inc.Namespace, strings.Join(lines, "\n")),
		Reversible: false,
	}}, true
}

// readySince returns how long a pod has been continuously ready.
func readySince(pod *corev1.Pod) time.Duration {
	if pod.Status.Phase != corev1.PodRunning {
		return 0
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return time.Since(cond.LastTransitionTime.Time)
		}
	}
	return 0
}

// oomEvents returns the kubelet's out-of-memory event count for the
// failing container, or 0 when it cannot be read.
func (p *Planner) oomEvents(ctx context.Context, c *fleet.Cluster, inc *incident.Incident) int {
	for _, name := range inc.Pods {
		pod, err := c.Pods.Pods(inc.Namespace).Get(name)
		if err != nil || pod.Spec.NodeName == "" {
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		n, err := c.OOMEvents(qctx, pod.Spec.NodeName, inc.Namespace, name, inc.Container)
		cancel()
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// probeChanged reports whether the latest rollout touched the failing
// container's probes or ports, as recorded by planRollback.
func probeChanged(a *incident.Action, _ *incident.Incident) bool {
	return a != nil && a.Params["probeChanged"] == "true"
}

// finish runs the proposed template change in preview or dry-run mode,
// attaching the diff, the precondition and any warnings.
func (p *Planner) finish(ctx context.Context, c *fleet.Cluster, inc *incident.Incident, a *incident.Action) Plan {
	mode := modePreview
	if p.DryRun {
		mode = modeDryRun
	}
	res, err := editTemplate(ctx, c.Client, inc.Namespace, a.TargetKind, a.TargetName, "", 0, mode, mutation(a))
	if err != nil {
		return manual("kbark planned to %s, but the API server would not accept it: %v", lowerFirst(a.Title), err)
	}
	a.TargetUID, a.TargetGeneration, a.Diff = res.UID, res.Generation, res.Diff
	if tool := managedBy(res.Meta); tool != "" {
		a.Warnings = append(a.Warnings, fmt.Sprintf("This workload is managed by %s. A live change will drift from its source and may be reverted on the next sync; make the same change at the source.", tool))
	}
	return Plan{Action: a}
}

// mutation returns the pod-template edit an action stands for. It is
// the single definition used for preview, dry-run and apply, so what is
// approved is what is applied.
func mutation(a *incident.Action) func(*corev1.PodTemplateSpec) error {
	switch a.Type {
	case incident.ActionSetMemoryLimit:
		return func(t *corev1.PodTemplateSpec) error {
			ctr := findContainer(t, a.Params["container"], a.Params["init"] == "true")
			if ctr == nil {
				return fmt.Errorf("container %q not found", a.Params["container"])
			}
			to, err := resource.ParseQuantity(a.Params["to"])
			if err != nil {
				return err
			}
			old := ctr.Resources.Limits[corev1.ResourceMemory]
			if ctr.Resources.Limits == nil {
				ctr.Resources.Limits = corev1.ResourceList{}
			}
			ctr.Resources.Limits[corev1.ResourceMemory] = to
			// Keep a Guaranteed pod Guaranteed.
			if req, ok := ctr.Resources.Requests[corev1.ResourceMemory]; ok && req.Cmp(old) == 0 {
				ctr.Resources.Requests[corev1.ResourceMemory] = to
			}
			return nil
		}
	case incident.ActionTuneProbe:
		return func(t *corev1.PodTemplateSpec) error {
			ctr := findContainer(t, a.Params["container"], false)
			if ctr == nil {
				return fmt.Errorf("container %q not found", a.Params["container"])
			}
			probe := probeOf(ctr, a.Params["probe"])
			if probe == nil {
				return fmt.Errorf("container %q has no %s probe", a.Params["container"], a.Params["probe"])
			}
			delay, err1 := strconv.Atoi(a.Params["initialDelaySeconds"])
			timeout, err2 := strconv.Atoi(a.Params["timeoutSeconds"])
			if err1 != nil || err2 != nil {
				return errors.New("invalid probe timing")
			}
			probe.InitialDelaySeconds, probe.TimeoutSeconds = int32(delay), int32(timeout)
			return nil
		}
	case incident.ActionRollbackWorkload:
		return func(t *corev1.PodTemplateSpec) error {
			var prev corev1.PodTemplateSpec
			if err := json.Unmarshal(a.Template, &prev); err != nil {
				return fmt.Errorf("stored revision template: %w", err)
			}
			*t = prev
			return nil
		}
	}
	return func(*corev1.PodTemplateSpec) error {
		return fmt.Errorf("action %q does not edit a pod template", a.Type)
	}
}

// --- uncordon-node ---

func (p *Planner) planUncordon(ctx context.Context, c *fleet.Cluster) Plan {
	nodes, _ := c.Nodes.List(labels.Everything())
	var cordoned []*corev1.Node
	for _, n := range nodes {
		if n.Spec.Unschedulable && nodeReady(n) {
			cordoned = append(cordoned, n)
		}
	}
	if len(cordoned) == 0 {
		return manual("The scheduler reports unschedulable nodes, but no Ready node is cordoned. The nodes may be tainted or not Ready instead.")
	}
	sort.Slice(cordoned, func(i, j int) bool { return cordoned[i].Name < cordoned[j].Name })
	n := cordoned[0]
	a := &incident.Action{
		Type:       incident.ActionUncordonNode,
		TargetKind: "Node", TargetName: n.Name, TargetUID: string(n.UID),
		Title:      fmt.Sprintf("Uncordon node %q so it accepts pods again", n.Name),
		Rationale:  fmt.Sprintf("Node %q is Ready but marked unschedulable, and the scheduler names that as the reason the pod cannot be placed. Uncordoning does not move any running pod.", n.Name),
		Risk:       "medium",
		Diff:       fmt.Sprintf("--- node/%s spec (current)\n+++ node/%s spec (proposed)\n-unschedulable: true\n+unschedulable: false\n", n.Name, n.Name),
		Warnings:   []string{"A node is usually cordoned on purpose, for maintenance. Confirm the maintenance is over before approving."},
		Reversible: true,
	}
	if p.DryRun {
		if err := patchUnschedulable(ctx, c, n.Name, false, true); err != nil {
			return manual("kbark planned to uncordon node %q, but the API server would not accept it: %v", n.Name, err)
		}
	}
	return Plan{Action: a}
}

func patchUnschedulable(ctx context.Context, c *fleet.Cluster, name string, value, dryRun bool) error {
	opts := metav1.PatchOptions{FieldManager: "kbark"}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	body := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, value)
	_, err := c.Client.CoreV1().Nodes().Patch(ctx, name, types.StrategicMergePatchType, []byte(body), opts)
	return err
}

func nodeReady(n *corev1.Node) bool {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// --- delete-pod ---

func (p *Planner) planDeletePods(c *fleet.Cluster, inc *incident.Incident) Plan {
	var refs, lines []string
	for _, name := range inc.Pods {
		pod, err := c.Pods.Pods(inc.Namespace).Get(name)
		if err != nil || pod.Status.Phase != corev1.PodFailed {
			continue
		}
		refs = append(refs, name+"="+string(pod.UID))
		lines = append(lines, fmt.Sprintf("-pod/%s  (Failed: %s)", name, pod.Status.Reason))
	}
	if len(refs) == 0 {
		return manual("The dead pods are already gone.")
	}
	target := strings.SplitN(refs[0], "=", 2)[0]
	title := fmt.Sprintf("Delete dead pod %q", target)
	if len(refs) > 1 {
		title = fmt.Sprintf("Delete %d dead pods of %s", len(refs), inc.Workload)
	}
	return Plan{Action: &incident.Action{
		Type:       incident.ActionDeletePod,
		TargetKind: "Pod", TargetName: target,
		Params:     map[string]string{"pods": strings.Join(refs, ",")},
		Title:      title,
		Rationale:  "These pods have already been terminated by the node and will never run again; only their records remain. Deleting the records changes nothing that is running. The condition that caused the termination (for example node memory or disk pressure) is a separate matter and is not addressed by this.",
		Risk:       "low",
		Diff:       fmt.Sprintf("--- namespace/%s pods (current)\n+++ namespace/%s pods (proposed)\n%s\n", inc.Namespace, inc.Namespace, strings.Join(lines, "\n")),
		Reversible: false,
	}}
}

// --- apply, rollback, settle ---

// Apply performs an approved action. It re-checks the precondition
// recorded at planning time and returns ErrPrecondition if the target
// has changed.
func Apply(ctx context.Context, c *fleet.Cluster, ns string, a *incident.Action) (*incident.Undo, error) {
	switch a.Type {
	case incident.ActionSetMemoryLimit, incident.ActionTuneProbe, incident.ActionRollbackWorkload:
		res, err := editTemplate(ctx, c.Client, ns, a.TargetKind, a.TargetName, a.TargetUID, a.TargetGeneration, modeApply, mutation(a))
		if err != nil {
			return nil, err
		}
		return &incident.Undo{PrevTemplate: res.Before, AppliedGeneration: res.NewGeneration}, nil

	case incident.ActionUncordonNode:
		n, err := c.Client.CoreV1().Nodes().Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if string(n.UID) != a.TargetUID || !n.Spec.Unschedulable {
			return nil, ErrPrecondition
		}
		if err := patchUnschedulable(ctx, c, a.TargetName, false, false); err != nil {
			return nil, err
		}
		return &incident.Undo{}, nil

	case incident.ActionDeletePod, incident.ActionRestartPod:
		deleted := 0
		for _, ref := range strings.Split(a.Params["pods"], ",") {
			name, uid, ok := strings.Cut(ref, "=")
			if !ok {
				continue
			}
			id := types.UID(uid)
			err := c.Client.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &id}})
			switch {
			case err == nil:
				deleted++
			case apierrors.IsNotFound(err), apierrors.IsConflict(err):
				// Already gone, or the name now belongs to a different pod.
			default:
				return nil, err
			}
		}
		if deleted == 0 {
			return nil, ErrPrecondition
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unknown action %q", a.Type)
}

// Rollback reverses an applied action using the saved Undo.
func Rollback(ctx context.Context, c *fleet.Cluster, ns string, a *incident.Action, undo *incident.Undo) error {
	if undo == nil {
		return errors.New("this action cannot be rolled back")
	}
	switch a.Type {
	case incident.ActionSetMemoryLimit, incident.ActionTuneProbe, incident.ActionRollbackWorkload:
		_, err := editTemplate(ctx, c.Client, ns, a.TargetKind, a.TargetName, a.TargetUID, undo.AppliedGeneration, modeApply,
			func(t *corev1.PodTemplateSpec) error {
				var prev corev1.PodTemplateSpec
				if err := json.Unmarshal(undo.PrevTemplate, &prev); err != nil {
					return err
				}
				*t = prev
				return nil
			})
		return err
	case incident.ActionUncordonNode:
		return patchUnschedulable(ctx, c, a.TargetName, true, false)
	}
	return errors.New("this action cannot be rolled back")
}

// Stale reports whether the object a proposal was computed from has
// changed, using the informer cache. A stale proposal must be re-planned.
func Stale(c *fleet.Cluster, ns string, a *incident.Action) bool {
	var meta metav1.Object
	switch a.TargetKind {
	case "Deployment":
		if o, err := c.Deployments.Deployments(ns).Get(a.TargetName); err == nil {
			meta = o
		}
	case "StatefulSet":
		if o, err := c.StatefulSets.StatefulSets(ns).Get(a.TargetName); err == nil {
			meta = o
		}
	case "DaemonSet":
		if o, err := c.DaemonSets.DaemonSets(ns).Get(a.TargetName); err == nil {
			meta = o
		}
	case "Node":
		n, err := c.Nodes.Get(a.TargetName)
		return err != nil || string(n.UID) != a.TargetUID || !n.Spec.Unschedulable
	default:
		return false
	}
	return meta == nil || string(meta.GetUID()) != a.TargetUID || meta.GetGeneration() != a.TargetGeneration
}

// Settled reports whether the cluster has finished reacting to an
// applied action, with a short description of what it is waiting for.
func Settled(c *fleet.Cluster, ns string, a *incident.Action) (bool, string) {
	switch a.TargetKind {
	case "Deployment":
		d, err := c.Deployments.Deployments(ns).Get(a.TargetName)
		if err != nil {
			return false, "deployment not found"
		}
		if !fleet.DeploymentRolledOut(d) {
			return false, fmt.Sprintf("rollout in progress: %d updated, %d available of %d", d.Status.UpdatedReplicas, d.Status.AvailableReplicas, replicas(d.Spec.Replicas))
		}
	case "StatefulSet":
		s, err := c.StatefulSets.StatefulSets(ns).Get(a.TargetName)
		if err != nil {
			return false, "statefulset not found"
		}
		want := replicas(s.Spec.Replicas)
		if s.Status.ObservedGeneration < s.Generation || s.Status.UpdatedReplicas != want || s.Status.ReadyReplicas != want {
			return false, fmt.Sprintf("rollout in progress: %d updated, %d ready of %d", s.Status.UpdatedReplicas, s.Status.ReadyReplicas, want)
		}
	case "DaemonSet":
		s, err := c.DaemonSets.DaemonSets(ns).Get(a.TargetName)
		if err != nil {
			return false, "daemonset not found"
		}
		want := s.Status.DesiredNumberScheduled
		if s.Status.ObservedGeneration < s.Generation || s.Status.UpdatedNumberScheduled != want || s.Status.NumberAvailable != want {
			return false, fmt.Sprintf("rollout in progress: %d updated, %d available of %d", s.Status.UpdatedNumberScheduled, s.Status.NumberAvailable, want)
		}
	}
	return true, ""
}

// HoldFor returns how long the failure must stay clear after an action
// before it counts as fixed. A relaxed probe proves nothing until the
// probe has had time to run and fail, so that action waits out the new
// initial delay plus a margin for a few probe periods.
func HoldFor(a *incident.Action, base time.Duration) time.Duration {
	if a.Type == incident.ActionTuneProbe {
		if delay, err := strconv.Atoi(a.Params["initialDelaySeconds"]); err == nil {
			return max(base, time.Duration(delay)*time.Second+30*time.Second)
		}
	}
	return base
}

func replicas(p *int32) int32 {
	if p == nil {
		return 1
	}
	return *p
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
