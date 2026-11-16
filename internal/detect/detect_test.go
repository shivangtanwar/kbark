// SPDX-License-Identifier: Apache-2.0

package detect

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var now = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) metav1.Time { return metav1.NewTime(now.Add(-d)) }

func basePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", CreationTimestamp: ago(5 * time.Minute)},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "busybox:1.36",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
				},
				LivenessProbe: &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt32(8080)}},
					InitialDelaySeconds: 5,
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func terminated(reason string, code int32, finished time.Duration) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, ExitCode: code, FinishedAt: ago(finished)}}
}

func waiting(reason, msg string) corev1.ContainerState {
	return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: msg}}
}

func running(started time.Duration) corev1.ContainerState {
	return corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: ago(started)}}
}

func event(reason, msg string, age time.Duration) *corev1.Event {
	return &corev1.Event{
		Reason: reason, Message: msg, Count: 4, LastTimestamp: ago(age),
		InvolvedObject: corev1.ObjectReference{FieldPath: "spec.containers{app}"},
	}
}

func TestPod(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(p *corev1.Pod)
		events     []*corev1.Event
		wantFamily Family // "" means healthy
		wantCause  string
		wantInit   bool
		wantText   string // substring expected in summary or evidence
	}{
		{
			name: "healthy running pod",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", Ready: true, State: running(time.Minute)}}
			},
		},
		{
			name: "oom killed and back off",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 4,
					State:                waiting("CrashLoopBackOff", "back-off"),
					LastTerminationState: terminated("OOMKilled", 137, time.Minute),
				}}
			},
			wantFamily: FamilyOOM, wantText: "64Mi",
		},
		{
			name: "oom killed but currently running between kills",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 2, Ready: true,
					State:                running(10 * time.Second),
					LastTerminationState: terminated("OOMKilled", 137, 20*time.Second),
				}}
			},
			wantFamily: FamilyOOM,
		},
		{
			name: "old oom on a pod that has been stable since is not a finding",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 1, Ready: true,
					State:                running(2 * time.Hour),
					LastTerminationState: terminated("OOMKilled", 137, 2*time.Hour),
				}}
			},
		},
		{
			name: "repeated exit 137 under a memory limit is an inferred oom",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 3,
					State:                waiting("CrashLoopBackOff", "back-off"),
					LastTerminationState: terminated("Error", 137, time.Minute),
				}}
			},
			wantFamily: FamilyOOM, wantCause: CauseInferred, wantText: "did not report OOMKilled",
		},
		{
			name: "exit 137 without a memory limit stays a crash",
			mutate: func(p *corev1.Pod) {
				p.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 3,
					State:                waiting("CrashLoopBackOff", "back-off"),
					LastTerminationState: terminated("Error", 137, time.Minute),
				}}
			},
			wantFamily: FamilyCrash,
		},
		{
			name: "crash loop with application exit code",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 6, Image: "busybox:1.36",
					State:                waiting("CrashLoopBackOff", "back-off"),
					LastTerminationState: terminated("Error", 1, time.Minute),
				}}
			},
			wantFamily: FamilyCrash, wantText: "code 1",
		},
		{
			name: "image tag not found",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", Image: "busybox:nope",
					State: waiting("ImagePullBackOff", `Back-off pulling image "busybox:nope"`),
				}}
			},
			events:     []*corev1.Event{event("Failed", `failed to resolve reference "docker.io/library/busybox:nope": not found`, time.Minute)},
			wantFamily: FamilyImagePull, wantCause: CauseBadReference,
		},
		{
			name: "image pull unauthorized",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", Image: "registry.example.com/private:1",
					State: waiting("ErrImagePull", "failed to authorize: 401 Unauthorized"),
				}}
			},
			wantFamily: FamilyImagePull, wantCause: CauseAuth,
		},
		{
			name: "missing configmap",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", State: waiting("CreateContainerConfigError", `configmap "reports-config" not found`),
				}}
			},
			wantFamily: FamilyConfig, wantText: `ConfigMap "reports-config" does not exist`,
		},
		{
			name: "missing secret key",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", State: waiting("CreateContainerConfigError", `couldn't find key password in Secret ns/db`),
				}}
			},
			wantFamily: FamilyConfig, wantText: `key "password" is missing from Secret ns/db`,
		},
		{
			name: "liveness probe kills a slow starter",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 3,
					State:                running(5 * time.Second),
					LastTerminationState: terminated("Error", 137, 10*time.Second),
				}}
			},
			events:     []*corev1.Event{event("Unhealthy", `Liveness probe failed: Get "http://10.0.0.1:8080/": dial tcp 10.0.0.1:8080: connect: connection refused`, 15*time.Second)},
			wantFamily: FamilyProbe, wantCause: CauseProbeRefused, wantText: "initialDelay=5s",
		},
		{
			name: "application crash wins over a coincidental probe event",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", RestartCount: 3,
					State:                waiting("CrashLoopBackOff", ""),
					LastTerminationState: terminated("Error", 2, 10*time.Second),
				}}
			},
			events:     []*corev1.Event{event("Unhealthy", "Liveness probe failed: connection refused", 15*time.Second)},
			wantFamily: FamilyCrash,
		},
		{
			name: "readiness probe failing on a running container",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: running(3 * time.Minute)}}
			},
			events:     []*corev1.Event{event("Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 503", 5*time.Second)},
			wantFamily: FamilyProbe, wantCause: CauseProbeReadiness,
		},
		{
			name: "readiness probe that times out is a timing problem",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: running(3 * time.Minute)}}
			},
			events:     []*corev1.Event{event("Unhealthy", "Readiness probe failed: command timed out: \"sh -c check\" timed out after 1s", 5*time.Second)},
			wantFamily: FamilyProbe, wantCause: CauseProbeTimeout,
		},
		{
			name: "image absent from the node with pull policy Never",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", Image: "busybox:1.35",
					State: waiting("ErrImageNeverPull", `Container image "busybox:1.35" is not present with pull policy of Never`),
				}}
			},
			wantFamily: FamilyImagePull, wantCause: CauseNeverPull, wantText: "pull policy is Never",
		},
		{
			name: "evicted for exceeding the pod's ephemeral-storage limit",
			mutate: func(p *corev1.Pod) {
				p.Spec.Containers[0].Resources.Limits[corev1.ResourceEphemeralStorage] = resource.MustParse("16Mi")
				p.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "Pod ephemeral local storage usage exceeds the total limit of containers 16Mi. "}
			},
			wantFamily: FamilyDisk, wantText: "16Mi ephemeral-storage limit",
		},
		{
			name: "evicted for exceeding a named container's ephemeral-storage limit",
			mutate: func(p *corev1.Pod) {
				p.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: `Container app exceeded its local ephemeral storage limit "16Mi". `}
			},
			wantFamily: FamilyDisk, wantText: `container "app" was evicted`,
		},
		{
			name: "pod-wide storage eviction with two limited containers stays a plain eviction",
			mutate: func(p *corev1.Pod) {
				lim := corev1.ResourceList{corev1.ResourceEphemeralStorage: resource.MustParse("8Mi")}
				p.Spec.Containers[0].Resources.Limits = lim
				p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "side", Resources: corev1.ResourceRequirements{Limits: lim}})
				p.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "Pod ephemeral local storage usage exceeds the total limit of containers 16Mi. "}
			},
			wantFamily: FamilyEvicted,
		},
		{
			name: "unready container without probe events is left alone",
			mutate: func(p *corev1.Pod) {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: running(3 * time.Minute)}}
			},
		},
		{
			name: "pending on a cordoned node",
			mutate: func(p *corev1.Pod) {
				unschedulable(p, "0/1 nodes are available: 1 node(s) were unschedulable. preemption: 0/1 nodes are available")
			},
			wantFamily: FamilyScheduling, wantCause: CauseCordoned,
		},
		{
			name: "pending on insufficient memory",
			mutate: func(p *corev1.Pod) {
				unschedulable(p, "0/3 nodes are available: 3 Insufficient memory.")
			},
			wantFamily: FamilyScheduling, wantCause: CauseInsufficient,
		},
		{
			name: "pending on node selector",
			mutate: func(p *corev1.Pod) {
				unschedulable(p, "0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector.")
			},
			wantFamily: FamilyScheduling, wantCause: CauseSelector,
		},
		{
			name: "pending on unbound claim is a storage finding",
			mutate: func(p *corev1.Pod) {
				unschedulable(p, "0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims.")
			},
			wantFamily: FamilyStorage,
		},
		{
			name: "just-created pending pod is within grace",
			mutate: func(p *corev1.Pod) {
				unschedulable(p, "0/1 nodes are available: 1 Insufficient cpu.")
				p.CreationTimestamp = ago(5 * time.Second)
			},
		},
		{
			name: "evicted pod",
			mutate: func(p *corev1.Pod) {
				p.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted", Message: "The node was low on resource: memory."}
			},
			wantFamily: FamilyEvicted, wantText: "low on resource",
		},
		{
			name: "failed job pod is the job's business",
			mutate: func(p *corev1.Pod) {
				p.OwnerReferences = []metav1.OwnerReference{{Kind: "Job", Name: "j"}}
				p.Status = corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded"}
			},
		},
		{
			name: "init container crash",
			mutate: func(p *corev1.Pod) {
				p.Spec.InitContainers = []corev1.Container{{Name: "migrate", Image: "busybox"}}
				p.Status.Phase = corev1.PodPending
				p.Status.InitContainerStatuses = []corev1.ContainerStatus{{
					Name: "migrate", RestartCount: 3,
					State:                waiting("CrashLoopBackOff", ""),
					LastTerminationState: terminated("Error", 1, 30*time.Second),
				}}
			},
			wantFamily: FamilyCrash, wantInit: true, wantText: "init container",
		},
		{
			name: "completed init container is healthy",
			mutate: func(p *corev1.Pod) {
				p.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "migrate", Ready: true, State: terminated("Completed", 0, time.Minute)}}
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", Ready: true, State: running(time.Minute)}}
			},
		},
		{
			name: "volume mount failure",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: waiting("ContainerCreating", "")}}
			},
			events:     []*corev1.Event{{Reason: "FailedMount", Message: `MountVolume.MountDevice failed for volume "pv-1": rpc error`, Count: 9, LastTimestamp: ago(10 * time.Second)}},
			wantFamily: FamilyStorage,
		},
		{
			name: "mount failure caused by a missing configmap is a config finding",
			mutate: func(p *corev1.Pod) {
				p.Status.Phase = corev1.PodPending
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: waiting("ContainerCreating", "")}}
			},
			events:     []*corev1.Event{{Reason: "FailedMount", Message: `MountVolume.SetUp failed for volume "cfg" : configmap "site" not found`, Count: 9, LastTimestamp: ago(10 * time.Second)}},
			wantFamily: FamilyConfig, wantText: `ConfigMap "site" does not exist`,
		},
		{
			name: "terminating pod is ignored",
			mutate: func(p *corev1.Pod) {
				t := ago(time.Second)
				p.DeletionTimestamp = &t
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: waiting("CrashLoopBackOff", ""), RestartCount: 9}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := basePod()
			tt.mutate(p)
			got := Pod(p, tt.events, now)
			if tt.wantFamily == "" {
				if len(got) != 0 {
					t.Fatalf("want healthy, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one finding, got %d: %+v", len(got), got)
			}
			f := got[0]
			if f.Family != tt.wantFamily {
				t.Errorf("family = %q, want %q", f.Family, tt.wantFamily)
			}
			if tt.wantCause != "" && f.Cause != tt.wantCause {
				t.Errorf("cause = %q, want %q", f.Cause, tt.wantCause)
			}
			if f.Init != tt.wantInit {
				t.Errorf("init = %v, want %v", f.Init, tt.wantInit)
			}
			if f.Summary == "" || len(f.Evidence) == 0 {
				t.Errorf("finding lacks summary or evidence: %+v", f)
			}
			if tt.wantText != "" {
				all := f.Summary
				for _, e := range f.Evidence {
					all += "\n" + e.Label + ": " + e.Value
				}
				if !strings.Contains(all, tt.wantText) {
					t.Errorf("want %q in:\n%s", tt.wantText, all)
				}
			}
		})
	}
}

func unschedulable(p *corev1.Pod, msg string) {
	p.Spec.NodeName = ""
	p.Status = corev1.PodStatus{
		Phase: corev1.PodPending,
		Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: msg,
		}},
	}
}

func TestCreation(t *testing.T) {
	failed := func(msg string, age time.Duration) []*corev1.Event {
		return []*corev1.Event{{Reason: "FailedCreate", Message: msg, Count: 12, LastTimestamp: ago(age)}}
	}
	tests := []struct {
		name      string
		events    []*corev1.Event
		wantCause string // "" means no finding
	}{
		{"no events", nil, ""},
		{"other events only", []*corev1.Event{{Reason: "SuccessfulCreate", Message: "Created pod: x", LastTimestamp: ago(time.Second)}}, ""},
		{"stale refusal", failed(`pods "x" is forbidden: exceeded quota: compute`, time.Hour), ""},
		{"quota", failed(`Error creating: pods "api-5d9-" is forbidden: exceeded quota: compute, requested: pods=1, used: pods=4, limited: pods=4`, time.Minute), CauseQuota},
		{"service account", failed(`Error creating: pods "api-5d9-" is forbidden: error looking up service account shop/ghost: serviceaccount "ghost" not found`, time.Minute), CauseServiceAccount},
		{"pod security", failed(`Error creating: pods "api-5d9-" is forbidden: violates PodSecurity "restricted:latest": allowPrivilegeEscalation != false`, time.Minute), CausePodSecurity},
		{"limit range", failed(`Error creating: pods "api-5d9-" is forbidden: maximum memory usage per Container is 1Gi, but limit is 2Gi`, time.Minute), CauseLimitRange},
		{"webhook", failed(`Error creating: admission webhook "validate.example.com" denied the request: no`, time.Minute), CauseWebhook},
		{"something else", failed(`Error creating: Internal error occurred`, time.Minute), CauseUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := Creation(tt.events, now)
			if tt.wantCause == "" {
				if ok {
					t.Fatalf("want no finding, got %+v", f)
				}
				return
			}
			if !ok || f.Family != FamilyAdmission || f.Cause != tt.wantCause {
				t.Fatalf("got ok=%v %+v, want cause %q", ok, f, tt.wantCause)
			}
			if f.Summary == "" || len(f.Evidence) == 0 {
				t.Errorf("finding lacks summary or evidence: %+v", f)
			}
		})
	}
}
