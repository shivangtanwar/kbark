// SPDX-License-Identifier: Apache-2.0

package detect

import (
	"fmt"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// Families that are not about a running pod's containers.
const (
	// FamilyNode: a node is not Ready or reports resource pressure.
	FamilyNode Family = "node"
	// FamilyTerminating: a pod was deleted and has not gone away.
	FamilyTerminating Family = "terminating"
	// FamilyJob: a Job gave up.
	FamilyJob Family = "job"
)

const (
	CauseNotReady       = "not-ready"
	CauseMemoryPressure = "memory-pressure"
	CauseDiskPressure   = "disk-pressure"
	CausePIDPressure    = "pid-pressure"

	// CauseNodeDown: the pod's node is gone or not Ready, so no kubelet
	// can confirm the container stopped. Set by the caller, which is
	// the one that can see nodes.
	CauseNodeDown  = "node-down"
	CauseFinalizer = "finalizer"

	CauseBackoffLimit = "backoff-limit"
	CauseDeadline     = "deadline"
)

const (
	// NodeGrace is how long a node may be not Ready before it is
	// reported. Kubelets miss a heartbeat now and then.
	NodeGrace = 60 * time.Second
	// StuckGrace is how far past its deletion deadline a pod may be
	// before it counts as stuck. The deadline already includes the
	// pod's own grace period.
	StuckGrace = 2 * time.Minute
	// JobWindow is how long a failed Job is reported. Failed Jobs are
	// kept for inspection long after anyone can act on them.
	JobWindow = time.Hour
)

// Node reports a node that is not Ready or is under resource pressure.
func Node(n *corev1.Node, now time.Time) (Finding, bool) {
	if n == nil || n.DeletionTimestamp != nil {
		return Finding{}, false
	}
	base := []Evidence{{"Kubelet", n.Status.NodeInfo.KubeletVersion}}
	if n.Spec.Unschedulable {
		base = append(base, Evidence{"Schedulable", "no (cordoned)"})
	}
	for _, c := range n.Status.Conditions {
		if c.Type != corev1.NodeReady || c.Status == corev1.ConditionTrue {
			continue
		}
		since := now.Sub(c.LastTransitionTime.Time)
		if since < NodeGrace {
			return Finding{}, false
		}
		state := "NotReady"
		if c.Status == corev1.ConditionUnknown {
			state = "NotReady (the kubelet has stopped reporting)"
		}
		ev := []Evidence{{"Node condition", state}, {"For", since.Round(time.Second).String()}}
		if c.Message != "" {
			ev = append(ev, Evidence{"Message", c.Message})
		}
		return Finding{
			Family: FamilyNode, Reason: "NodeNotReady", Cause: CauseNotReady,
			Summary:  "node is not Ready, so its pods are not being run or reported",
			Evidence: append(ev, base...),
		}, true
	}
	pressure := map[corev1.NodeConditionType][2]string{
		corev1.NodeMemoryPressure: {CauseMemoryPressure, "memory"},
		corev1.NodeDiskPressure:   {CauseDiskPressure, "disk space"},
		corev1.NodePIDPressure:    {CausePIDPressure, "process IDs"},
	}
	for _, c := range n.Status.Conditions {
		p, ok := pressure[c.Type]
		if !ok || c.Status != corev1.ConditionTrue {
			continue
		}
		ev := []Evidence{{"Node condition", string(c.Type)}, {"For", now.Sub(c.LastTransitionTime.Time).Round(time.Second).String()}}
		if c.Message != "" {
			ev = append(ev, Evidence{"Message", c.Message})
		}
		return Finding{
			Family: FamilyNode, Reason: string(c.Type), Cause: p[0],
			Summary:  fmt.Sprintf("node is short of %s and is evicting pods", p[1]),
			Evidence: append(ev, base...),
		}, true
	}
	return Finding{}, false
}

// Terminating reports a pod that was deleted and is still there well
// past its deadline. The cause is CauseFinalizer when finalizers hold
// it; otherwise the caller decides between CauseNodeDown and unknown.
func Terminating(pod *corev1.Pod, now time.Time) (Finding, bool) {
	if pod == nil || pod.DeletionTimestamp == nil {
		return Finding{}, false
	}
	overdue := now.Sub(pod.DeletionTimestamp.Time)
	if overdue < StuckGrace {
		return Finding{}, false
	}
	f := Finding{
		Family: FamilyTerminating, Reason: "Terminating", Cause: CauseUnknown,
		Summary: "pod was deleted but has not gone away",
		Evidence: []Evidence{
			{"Pod state", "Terminating"},
			{"Past its deletion deadline by", overdue.Round(time.Second).String()},
		},
	}
	if pod.Spec.NodeName != "" {
		f.Evidence = append(f.Evidence, Evidence{"Node", pod.Spec.NodeName})
	}
	if len(pod.Finalizers) > 0 {
		fin := append([]string(nil), pod.Finalizers...)
		sort.Strings(fin)
		f.Cause = CauseFinalizer
		f.Summary = "pod was deleted but a finalizer is holding it"
		f.Evidence = append(f.Evidence, Evidence{"Finalizers", strings.Join(fin, ", ")})
	}
	return f, true
}

// Job reports a Job that has given up: it ran out of retries or time.
func Job(job *batchv1.Job, now time.Time) (Finding, bool) {
	if job == nil || job.DeletionTimestamp != nil {
		return Finding{}, false
	}
	for _, c := range job.Status.Conditions {
		if c.Type != batchv1.JobFailed || c.Status != corev1.ConditionTrue {
			continue
		}
		if now.Sub(c.LastTransitionTime.Time) > JobWindow {
			return Finding{}, false
		}
		f := Finding{
			Family: FamilyJob, Reason: orDefault(c.Reason, "Failed"), Cause: CauseUnknown,
			Summary: "job failed",
			Evidence: []Evidence{
				{"Job condition", "Failed: " + c.Reason},
				{"Failed pods", fmt.Sprint(job.Status.Failed)},
				{"Failed at", c.LastTransitionTime.UTC().Format(time.RFC3339)},
			},
		}
		if c.Message != "" {
			f.Evidence = append(f.Evidence, Evidence{"Message", c.Message})
		}
		if len(job.Spec.Template.Spec.Containers) > 0 {
			f.Container = job.Spec.Template.Spec.Containers[0].Name
		}
		switch c.Reason {
		case "BackoffLimitExceeded":
			limit := int32(6)
			if job.Spec.BackoffLimit != nil {
				limit = *job.Spec.BackoffLimit
			}
			f.Cause = CauseBackoffLimit
			f.Summary = fmt.Sprintf("job gave up after its pods failed more than %d time(s)", limit)
		case "DeadlineExceeded":
			f.Cause = CauseDeadline
			f.Summary = "job was stopped because it ran past its deadline"
		}
		return f, true
	}
	return Finding{}, false
}
