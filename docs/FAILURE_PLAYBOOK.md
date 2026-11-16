# Kubernetes Pod Failures: Problems, Causes and How kbark Fixes Them

This document has three parts. Part A records the problems with kbark's
original analysis-only design that led to the autofix scope. Part B records
the problems met while building and testing the autofix layer, and how each
was resolved. Part C covers each class of Kubernetes pod failure: what it
looks like, what causes it, how kbark detects it, and what kbark does about it.

The design is described in [AUTOFIX_PLAN.md](AUTOFIX_PLAN.md).

## Part A — Problems with the analysis-only approach

These are limitations of kbark v0.1 as it exists in the repository.

| # | Problem | Where it comes from | How the autofix layer addresses it |
|---|---|---|---|
| 1 | Nothing happens unless someone is looking. A failure is diagnosed only when a person selects that resource and presses `?`. | Diagnosis is a key handler in the terminal UI | Informers watch every pod in every configured cluster; incidents open on their own |
| 2 | The language model is the classifier. The same pod can get differently worded, and sometimes different, answers on two runs; each look costs tokens; without a provider there is no diagnosis. | `diagnose.Session` sends context and streams back prose | Classification is deterministic Go code. The model only explains evidence already established |
| 3 | The evidence is generic. The context holds phase, regular container states, the ten most recent events and one 200-line log tail. Init containers, probe definitions, resource limits, pod conditions and the owning workload are not included up front. | `diagnose.PodContextBuilder` | Each detector reads the fields that matter for its failure family and records them as evidence |
| 4 | The answer is prose. It cannot be previewed as a change, applied, verified or undone. | Free-form streamed text | Fixes are typed actions with a dry-run diff |
| 5 | kbark cannot act. It is read-only by contract, and its prompts were tuned to avoid even suggesting commands. The person must translate the explanation into `kubectl` themselves. | `kube.AllowedVerbs`; prompt tuning in M9.3 | A small write allowlist, used only after approval |
| 6 | Nobody checks whether the problem went away. | No post-diagnosis step | Verification window after every fix; rollback if it did not help |
| 7 | One terminal, one cluster. | Single clientset from the current context | One watcher set per kubeconfig context, one dashboard |
| 8 | No operational record. Transcripts are saved per diagnosis but there is no list of open problems, who handled them, or what changed. | `internal/transcript` | Incident lifecycle and append-only audit log |
| 9 | The mid-semester design (evidence bundles, evidence IDs, 150 scored outputs) measured the quality of explanations, not whether the cluster got healthy. | Proposal sections 3 and 6 | Success is measured by recovery: detected, fixed, verified, time to recover |

## Part B — Problems met while building the autofix layer

Each of these was observed on the Kind clusters during development on
16 November 2026.

| # | Problem observed | Cause | Resolution |
|---|---|---|---|
| 1 | On the laptop's Kind clusters (the server's runtime does report it), an out-of-memory kill was never reported as `OOMKilled`. The fixture was killed by the kernel (the pod's cgroup counter `memory.events` showed `oom_kill 8`) but the container status said `reason: Error, exitCode: 137`. The kubelet's `container_oom_events_total` metric stayed at 0 too. | The container runtime in this Kind setup (containerd 2.2 inside a Docker container) does not surface the kernel's OOM event. Managed clusters normally do. | The exact detector (`reason == OOMKilled`) is kept, and a second, labelled path was added: exit 137, a memory limit on the container, repeated restarts, and no probe kill. Such findings carry the cause `inferred`, say so in the evidence, and the proposal carries a warning. |
| 2 | A crash-looping workload opened and closed incidents repeatedly. | Between crashes the container is `Running` for a few seconds, so the finding disappears and reappears. | Two measures. Detectors treat a termination in the last 10 minutes as current. The store closes an incident only after the failure has been absent for 45 seconds. |
| 3 | Binding approval to the object's `resourceVersion` would have made approval impossible on exactly the workloads that need it. | A failing Deployment's status is rewritten constantly, and every status write changes `resourceVersion`. | Approval is bound to UID and `metadata.generation`, which changes only when the spec changes. |
| 4 | A probe fix was declared verified 18 seconds after it was applied, before the relaxed probe had run once. | The verification hold was a fixed 15 seconds; the new initial delay was 30. | The hold now depends on the action: a probe fix must stay clear for its new initial delay plus 30 seconds. The same run now verifies after 60 seconds with zero restarts. |
| 5 | After a memory fix was rolled back, the workload was first classified as an application crash, then reclassified as out-of-memory one restart later, leaving a spurious closed incident. | The inferred out-of-memory path required two restarts; the crash path required none. | The inferred path now applies from the first restart. |
| 6 | The first Service detector reported every pod in the namespace as a near match. | It compared label keys only, and every pod has an `app` label. | A pod is a near match only if every selector value equals the pod's value once case and punctuation are ignored (`check-out` and `checkout`). |
| 7 | `kind load docker-image busybox:1.36` failed with "content digest not found". | Kind cannot import some multi-platform images from Docker's image store. | Preloading in `demo/demo.sh` is best-effort; on failure the node pulls the image itself. The demo therefore needs a network connection on first run. |
| 8 | Every AI explanation failed with HTTP 401. | The OpenAI API key in the development environment was invalid. | Nothing needed fixing in the pipeline: detection, proposals, approval and verification all completed without the provider, which is the intended property. The dashboard shows the provider error in one line. A working key is needed for explanations in the demonstration. |
| 9 | Re-applying a fixture to a Deployment kbark had fixed produced a rollback proposal instead of the probe-timing proposal the fixture was meant to show. | The re-apply created a new revision that reverted kbark's fix, and the previous revision (with the fix) was healthy. kbark's choice was correct. | No change to kbark. `demo/demo.sh reset` deletes the namespace so every scenario starts from a single revision. |
| 11 | The first trend chart showed twice the real number of pods at some points. | Two samples from the same cluster landed in the same 30-second bucket and were added together. | The console keeps one sample per cluster per bucket and sums across clusters only. |
| 12 | The approval dialog appeared off-centre, pinned inside the page column. | An ancestor element with a CSS transform (the page transition) becomes the containing block for `position: fixed`. | Dialogs are rendered at the document root. |
| 13 | Putting the console on a public server exposed two gaps in the first design: the operator name was whatever the browser sent, and a lockout keyed on the socket address would have treated every visitor behind nginx as one client. | The first version assumed a trusted localhost user. | Accounts with roles and server-side sessions; the audit name comes from the account. With `--trust-proxy` the lockout uses the client address nginx reports. kbark refuses to listen publicly without sign-in. |
| 14 | The server's default limit of 128 inotify instances is below what two Kind clusters like. | A kernel default sized for desktops. | Both clusters did start; the root install script raises the limit to 512 so it is not marginal. |
| 15 | After a namespace was deleted and recreated, an incident stayed "no automatic fix" with the reason "deployment not found", although the Deployment was back. | kbark re-planned during the seconds the Deployment did not exist, and incidents without a fix were never planned again. | Such incidents are planned again every 30 seconds while the failure is observed; the record changes only if the answer does. |
| 16 | An AI explanation said the memory request "remains 64Mi" when the fix raises it with the limit. | The model was given the fix's title and rationale, not the change itself. | The exact diff is now part of what the model sees. This is also a reminder of why the model only advises: the diff the approver signs was correct throughout. |
| 10 | Real evictions cannot be produced safely on a one-node Kind cluster. | Eviction needs a node under memory or disk pressure. | The `evicted` scenario creates a pod that is never scheduled and writes the status the kubelet writes on eviction. kbark's detector and delete action run exactly as they would after a real eviction. |

Other difficulties the team met outside this build (earlier milestones,
environment setup) should be added here by the team; they cannot be
recovered from the repository.

## Part C — Failure families

Two rules apply to every family:

- kbark fixes the owning workload, not the pod, because controllers replace
  pods from the workload's template. A pod with no controller gets no
  automatic fix, except deletion of a dead record.
- When the correct fix requires information kbark cannot know (a missing
  credential, an application bug, the right configuration value), kbark
  marks the incident **Manual**, shows the evidence and explanation, and
  makes no change. It does not guess.

### Summary

| # | Family | Typical status shown | Automatic fix (after approval) |
|---|---|---|---|
| 1 | Out of memory | `OOMKilled`, exit 137 | Raise memory limit |
| 2 | Application crash | `CrashLoopBackOff`, non-zero exit | Replace the pod if its siblings are healthy; roll back if the failing pods are on the latest rollout; otherwise Manual |
| 3 | Image pull | `ErrImagePull`, `ImagePullBackOff` | Roll back if the latest rollout changed the image; otherwise Manual |
| 4 | Missing configuration | `CreateContainerConfigError` | Roll back if the failing pods are on the latest rollout; otherwise Manual |
| 5 | Init container failure | `Init:CrashLoopBackOff`, `Init:Error` | Same as the matching family above |
| 6 | Health probe failure | Restarts with `Unhealthy` events; `Ready=False` | Replace the pod if its siblings are healthy; relax probe timing for slow starts; roll back if the latest rollout changed the probe |
| 7 | Scheduling | `Pending`, `FailedScheduling` | Uncordon a cordoned node; roll back a bad rollout; otherwise Manual |
| 8 | Storage | `Pending` or `ContainerCreating` with `FailedMount` | Manual |
| 9 | Eviction and node pressure | `Evicted`, `Failed` | Delete the dead pod records; node pressure itself is not addressed |
| 10 | Network and service | Running but unreachable | Manual |

Family 5 is not a separate detector: the detectors for families 1 to 4 run
over init containers as well and mark the finding as an init container.

### 1. Out of memory (OOMKilled)

**What happens.** The container uses more memory than its limit. The kernel
kills it, the kubelet restarts it, and it is killed again. The application
log usually ends abruptly with no error.

**Common causes.** Limit set too low for the workload; a memory leak; a
runtime (for example the JVM) sized larger than the container limit; a load
spike.

**Detection.** Exact: the container's last termination has
`reason == OOMKilled`. Inferred (Part B, item 1): exit code 137, a memory
limit on the container, at least one restart, and no liveness or startup
probe kill recorded. Evidence recorded: last termination, current limit and
request, restart count, and for the inferred path a line saying the runtime
did not report `OOMKilled`.

**Fix.** `set-memory-limit` on the owning workload: double the limit, never
beyond the policy's memory ceiling (default 2Gi). With `--confirm-oom`, kbark
first asks the node's kubelet for its out-of-memory event counter; a non-zero
count turns an inferred finding into a confirmed one and drops the warning.
That check needs `get` on `nodes/proxy`, so it is off by default, and the
Kind runtime never reports such events. If the memory request equalled the
old limit it follows the new one, so a Guaranteed pod stays Guaranteed;
otherwise the request is left alone. Verified when the rollout completes and
the finding stays clear.

**Limits of the fix.** A genuine leak will hit the new limit later. After two
applied fixes to one workload within an hour kbark stops proposing and marks
the incident Manual. A container with no memory limit is Manual: it was the
node that ran out.

### 2. Application crash (CrashLoopBackOff)

**What happens.** The process starts and exits with an error. The kubelet
restarts it with growing back-off delays (up to five minutes), so the pod is
mostly down.

**Common causes.** A bug or unhandled exception at startup; a required
environment variable or file missing; a dependency (database, API) refusing
connections; a wrong command or arguments.

**Detection.** The container is waiting with `CrashLoopBackOff`,
`RunContainerError` or `CreateContainerError`, or has exited non-zero at
least twice recently. Evidence: exit code, restart count, image. The tail of
the previous container log is sent to the AI explainer.

**Fix.** Three cases, checked in this order.

1. Some pods of the workload crash while others on the *same revision* have
   been ready for at least five minutes: `restart-pod`. The spec is evidently
   sound, so the problem is local to those pods, and a fresh pod clears it.
2. The failing pods belong to the latest revision and an earlier one exists
   (a ReplicaSet for a Deployment, a ControllerRevision for a StatefulSet or
   DaemonSet): `rollback-workload`.
3. Otherwise Manual: restarting every pod of a crash-looping workload achieves
   nothing, and the cause is in the application or its dependencies.

### 3. Image pull failure (ErrImagePull, ImagePullBackOff)

**What happens.** The node cannot download the image, so the container never
starts.

**Common causes.** Typo in image name or tag; the tag was never pushed;
private registry without a valid `imagePullSecret`; registry unreachable or
rate-limiting.

**Detection.** The container is waiting with `ErrImagePull`,
`ImagePullBackOff`, `InvalidImageName` or `ErrImageNeverPull`. The kubelet
message and pull events are classified as a bad reference ("not found",
"manifest unknown"), a credential problem ("unauthorized", "denied") or a
registry problem (timeouts, DNS failures, rate limits).

**Fix.** If the latest rollout changed this container's image and an earlier
revision exists, `rollback-workload`. Otherwise Manual, with a reason that
matches the classification. kbark cannot create credentials and must not.

### 4. Missing configuration (CreateContainerConfigError)

**What happens.** The pod references a ConfigMap, Secret or key that does not
exist, so the kubelet cannot build the container.

**Common causes.** The ConfigMap or Secret was not deployed to this
namespace; a key was renamed; the workload was deployed before its
configuration.

**Detection.** The container is waiting with `CreateContainerConfigError`,
or the pod is stuck creating with a `FailedMount` event that names a missing
ConfigMap or Secret. The missing object or key is parsed from the kubelet's
message. kbark does not read ConfigMaps or Secrets itself and has no
permission to.

**Fix.** If the failing pods are on the latest rollout and an earlier
revision exists, `rollback-workload`. Otherwise Manual, naming the missing
object. kbark does not create configuration, because it cannot know the
right values. When someone creates the object, the incident closes on its
own.

### 5. Init container failure

**What happens.** An init container fails, so the main containers never
start. Status shows `Init:Error` or `Init:CrashLoopBackOff`.

**Common causes.** A migration or setup script failing; waiting for a
dependency that is not there; wrong permissions on a volume.

**Detection.** The checks for families 1 to 4, applied to
`initContainerStatuses`. v0.1 did not look at init containers at all.

**Fix.** As for the matching family: memory raise for an OOM-killed init
container, rollback for a failure on the latest rollout, otherwise Manual.

### 6. Health probe failure

**What happens.** A liveness or startup probe fails and the kubelet kills a
container that may be healthy; or a readiness probe fails and the pod runs
but receives no traffic.

**Common causes.** The application starts more slowly than
`initialDelaySeconds` allows; timeout too short under load; probe pointing
at the wrong port or path; the application really is unhealthy.

**Detection.** Liveness or startup: a recent `Unhealthy` event naming the
probe, at least one restart, and a last exit code that is a signal exit
(137, 143) or 0. An application that exits with its own error code is
classified as a crash even if a probe event exists. Readiness: the container
has been running but not ready for over a minute with recent readiness
`Unhealthy` events. Evidence: the probe definition, the event message and
its count.

**Fix.** If sibling pods on the same revision are healthy, `restart-pod` as in
family 2. Otherwise, liveness or startup failures with timeouts or refused connections:
`tune-probe`, which doubles the initial delay (at least 30 seconds, at most
300) and sets the timeout to at least 5 seconds. If the latest rollout
changed the probe or the container's ports, `rollback-workload` is preferred.
Probes answered with an HTTP error by the application, and readiness
failures with no earlier revision: Manual.

### 7. Scheduling failure (Pending)

**What happens.** No node can accept the pod, so it stays `Pending`.

**Common causes.** CPU or memory requests larger than any node has free; a
node selector or affinity that matches no node; a taint with no matching
toleration; nodes cordoned for maintenance and never uncordoned; an unbound
volume claim (see family 8).

**Detection.** Condition `PodScheduled=False` with reason `Unschedulable`
for more than 20 seconds. The scheduler's message is classified as cordoned,
insufficient resources, selector or affinity mismatch, or untolerated taint.

**Fix.** A Ready node is cordoned: `uncordon-node`, with a warning that
nodes are usually cordoned on purpose. Failing pods on the latest rollout of
a Deployment with an earlier revision: `rollback-workload`. Real lack of
capacity or placement rules with no earlier revision: Manual; adding nodes is
outside kbark's authority.

### 8. Storage failure

**What happens.** The pod waits for a volume: `Pending` while its claim is
unbound, or `ContainerCreating` with `FailedMount` or `FailedAttachVolume`.

**Common causes.** No StorageClass or a wrong name; no capacity; a
ReadWriteOnce volume still attached to another node; zone mismatch between
volume and node.

**Detection.** A scheduler message about PersistentVolumeClaims, or
`FailedMount` / `FailedAttachVolume` events on a pod stuck creating.

**Fix.** Manual. Storage changes can lose data, so kbark explains and stops.

### 9. Eviction and node pressure

**What happens.** A node runs short of memory or disk and the kubelet evicts
pods. Evicted pods stay in the list as `Failed` and accumulate; the
controller starts replacements elsewhere.

**Common causes.** Pods without requests packed onto a node; logs or
ephemeral storage filling the disk; a noisy neighbour.

**Detection.** Pod phase `Failed` with a status reason (`Evicted`,
`Shutdown` and similar), excluding pods owned by a Job.

**Fix.** `delete-pod` for the dead records, with a UID precondition so a
different pod of the same name is never deleted. This is the one action that
cannot be undone, and it changes nothing that is running. The pressure on
the node is not addressed.

### 10. Network and service failure

**What happens.** Pods are Running and Ready but callers get connection
errors or timeouts.

**Common causes.** Service selector does not match pod labels, so the
Service has no endpoints; wrong target port; a NetworkPolicy blocking
traffic; DNS failures.

**Detection.** Only the first cause is detected: a Service whose selector
matches no pod while some pod's labels are a near miss for it. Wrong ports,
NetworkPolicies and DNS failures are not detected.

**Fix.** Manual, with the selector and the near-miss pods shown. kbark
cannot tell which side is wrong.

## Why "roll back" appears so often

Most production failures follow a change. When the failing pods all belong
to the latest revision of a Deployment and an earlier revision exists,
returning to it restores the last configuration before the failure without
kbark needing to understand the application. It is also exactly reversible.
kbark therefore prefers rollback whenever revision history supports it, and
declines to invent a forward fix when it does not. kbark does not know that
the earlier revision was healthy; verification after the rollback is what
establishes that.

## Fix-outcome table

Runs on the Kind clusters, 16 November 2026. "Time to recover" is from
detection to verification and includes the time the proposal waited for
approval (a few seconds to about 45 seconds in these runs) and the
verification hold (15 seconds; 60 for the probe fix).

| Scenario | Cluster | Detected as | Fix approved | Outcome | Time to recover |
|---|---|---|---|---|---|
| `oom` | kbark-staging | Out of memory (inferred) | Memory limit 64Mi to 128Mi | Verified | 45s |
| `oom`, after rolling the fix back | kbark-staging | Out of memory (inferred) | Memory limit 64Mi to 128Mi | Verified | 1m1s |
| `crash` | kbark-dev | Application crash | Roll back revision 2 to 1 | Verified | 48s |
| `bad-image` | kbark-dev | Image pull, bad reference | Roll back revision 2 to 1 | Verified | 1m1s |
| `slow-start` | kbark-dev | Liveness probe, connection refused | Initial delay 5s to 30s, timeout 1s to 5s | Verified | 1m6s |
| `evicted` | kbark-dev | Evicted pod | Delete dead pod | Verified | 47s |
| `cordon` | kbark-staging | Scheduling, cordoned node | Uncordon node | Verified | 30s |
| `missing-config` | kbark-dev | Missing ConfigMap `reports-config` | None (Manual) | Cleared on its own after the ConfigMap was created | — |
| `selector` | kbark-dev | Service selector matches no pod | None (Manual) | Still open | — |
| `oom`, kbark running in-cluster | kbark-staging | Out of memory (inferred) | Memory limit 64Mi to 128Mi | Verified | 38s |
| `cordon`, kbark running in-cluster | kbark-staging | Scheduling, cordoned node | Uncordon node | Verified | 17s |
| `evicted`, kbark running in-cluster | kbark-staging | Evicted pod | Delete dead pod | Verified | 38s |
| `crash`, kbark on the server (demo.kbark.dev host) | kbark-dev | Application crash | Roll back revision 2 to 1 | Verified | 20s |
| `oom`, kbark on the server | kbark-staging | Out of memory (inferred) | Memory limit 64Mi to 128Mi | Verified | 39s |
| `bad-image`, kbark on the server | kbark-staging | Image pull, bad reference | None: the server's policy forbids rollback on staging | Manual, with the policy named | — |

Two older bare pods in the `default` namespace of kbark-dev (`cause-crash`,
`bad-image`, the v0.1 demo fixtures) were also detected and correctly marked
Manual, because a pod without a controller cannot be fixed in place.

No approved fix failed verification in these runs. The failed-verification
path is covered by an automated test (`TestFixThatDoesNotHelpIsMarkedFailed`).
