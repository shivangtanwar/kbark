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
| 17 | A StatefulSet rollback would have restored the template and then failed verification. | With ordered pod management a StatefulSet does not replace a broken pod after its template is reverted; Kubernetes documents this. Found while writing the live test, before running it. | The rollback proposal for a StatefulSet now names the broken pods and deletes them after the template is restored. Run live: `ledger-1` came back on revision 1. |
| 18 | Raising a disk limit could never be verified, because the evicted pods stay in the list as `Failed` and kept the finding alive. | One finding covered two different facts: "the limit is too low" and "dead records exist". | They are separate families. An eviction counts as a disk-limit failure only while the workload still has the limit the pod was evicted under; afterwards the pod is a dead record with its own delete proposal. |
| 19 | From outside the host, the console took several seconds to appear. | The 700 KB script was sent uncompressed; nginx was not configured to compress and neither was kbark. | kbark compresses scripts and styles itself (206 KB on the wire), so it does not depend on the proxy. |
| 20 | During those seconds the Incidents page said "No incidents match". | The page treated "no data yet" as "no incidents". | A loading placeholder is shown until the first snapshot arrives. |
| 21 | An explanation quoted "unable to retrieve container logs" as if it were the application's output. | When a container's log file is gone, the kubelet answers successfully with that sentence as the body. | That body is no longer treated as a log. |
| 22 | A second kind of cluster could not start on the laptop: every k3s node failed with "too many open files". | The kernel's default of 128 inotify instances per user was already used up by the two Kind clusters. Raising it needs root. | One Kind cluster is paused while the k3s cluster runs on the laptop. The server's limit had already been raised by the setup script. |
| 23 | On the server, k3d could not create its cluster on the Docker network Kind uses. | Not investigated further; the node container stopped logging within a second. | The k3s cluster gets its own network and the kbark container joins both. |
| 24 | After a stopped k3s worker was started again it never rejoined the cluster on the laptop. | A k3d limitation with restarted agent containers, not something kbark does. | The "node comes back" path was exercised by removing the node object instead; the node incident closed. |
| 25 | An engine test began failing with "directory not empty" when the machine was busy. | The engine writes its state file once more as it stops, and the test's temporary directory was being removed at the same moment. | The tests now wait for the engine to stop. |
| 10 | Real evictions cannot be produced safely on a one-node Kind cluster. | Eviction needs a node under memory or disk pressure. | The `evicted` scenario creates a pod that is never scheduled and writes the status the kubelet writes on eviction. kbark's detector and delete action run exactly as they would after a real eviction. Evictions for exceeding a container's own ephemeral-storage limit are real on Kind, and the `disk` scenario uses them. |

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
| 3 | Image pull | `ErrImagePull`, `ImagePullBackOff`, `ErrImageNeverPull` | Roll back if the latest rollout changed the image; allow the pull if the policy is `Never` and the image is absent; otherwise Manual |
| 4 | Missing configuration | `CreateContainerConfigError` | Roll back if the failing pods are on the latest rollout; otherwise Manual |
| 5 | Init container failure | `Init:CrashLoopBackOff`, `Init:Error` | Same as the matching family above |
| 6 | Health probe failure | Restarts with `Unhealthy` events; `Ready=False` | Replace the pod if its siblings are healthy; relax probe timing for slow starts and for readiness checks that time out; roll back if the latest rollout changed the probe |
| 7 | Scheduling | `Pending`, `FailedScheduling` | Uncordon a cordoned node; roll back a bad rollout; otherwise Manual |
| 8 | Storage | `Pending` or `ContainerCreating` with `FailedMount` | Manual |
| 9 | Eviction and node pressure | `Evicted`, `Failed` | Delete the dead pod records; node pressure itself is not addressed |
| 10 | Network and service | Running but unreachable | Correct a Service selector that nearly matches the pods of one workload; otherwise Manual |
| 11 | Local disk limit | `Evicted`, message names ephemeral storage | Raise the ephemeral-storage limit; the dead records are then handled as family 9 |
| 12 | Pods refused at creation | Fewer pods than wanted, `FailedCreate` on the controller | Roll back if an accepted revision exists; otherwise Manual (always Manual for a full quota) |
| 13 | Node not healthy | Node `NotReady`, or memory, disk or PID pressure | Manual: kbark does not repair nodes |
| 14 | Pod stuck terminating | `Terminating` long past its deadline | Force-delete, only when the pod's node is down; otherwise Manual |
| 15 | Failed Job | Job condition `Failed` | Manual: a Job cannot be changed once it exists |

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
revision exists, `rollback-workload`. If the status is `ErrImageNeverPull`
(the image is not on the node and the pull policy `Never` forbids fetching
it), `set-image-pull-policy` changes the policy to `IfNotPresent`, which
behaves identically once the image is present. The proposal warns that
`Never` is sometimes deliberate, for images loaded onto nodes by hand.
Otherwise Manual, with a reason that matches the classification. kbark
cannot create credentials and must not.

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
A readiness probe whose check times out (the event says "timed out" or
"deadline exceeded") is also a timing problem: `tune-probe` raises only its
timeout (1 second to 5, at most 30) and leaves everything else alone.
Probes answered with an HTTP error by the application, and readiness
failures of any other kind with no earlier revision: Manual. A longer
deadline would only hide an application that answers "not ready".

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
the node is not addressed. An eviction whose message names the container's
own ephemeral-storage limit is a different failure with a real fix; see
family 11.

### 10. Network and service failure

**What happens.** Pods are Running and Ready but callers get connection
errors or timeouts.

**Common causes.** Service selector does not match pod labels, so the
Service has no endpoints; wrong target port; a NetworkPolicy blocking
traffic; DNS failures.

**Detection.** Only the first cause is detected: a Service whose selector
matches no pod while some pod's labels are a near miss for it. Wrong ports,
NetworkPolicies and DNS failures are not detected.

**Fix.** `fix-service-selector`, when the answer is unambiguous: every
near-miss pod belongs to one workload and they agree on the label values.
The Service's selector is changed to those values; nothing restarts, and the
previous selector is stored for an exact rollback. kbark cannot know which
side holds the typing mistake, and the proposal says so: if the selector is
what was intended, the approver rejects it and corrects the workload. When
pods of two workloads nearly match, or they disagree, the incident is
Manual. The write is guarded by the Service's UID and by its selector still
being the one that was shown.

### 11. Local disk limit (ephemeral storage)

**What happens.** A container writes more to local disk (its writable
layer, logs, `emptyDir` volumes) than its `ephemeral-storage` limit. The
kubelet evicts the pod; the controller starts a replacement, which fills up
and is evicted in turn. Dead pod records pile up, one per cycle.

**Common causes.** A limit sized for an empty cache; unrotated logs; a
spool or temporary directory that grows with load.

**Detection.** A `Failed` pod with reason `Evicted` whose message says the
container (or the pod, when exactly one container has a limit) exceeded its
local ephemeral storage limit, while the workload still gives that container
the same limit. Once the workload's limit has changed, the same dead pods
are only records, and they are reported as family 9 instead.

**Fix.** `set-ephemeral-storage-limit` doubles the limit, up to a 2Gi
ceiling, keeping a request that equalled the limit equal to it. The fix is
held for a minute before it counts as verified, because a container needs
time to fill its disk again. After it holds, kbark opens a separate incident
proposing `delete-pod` for the records that were left behind: two approvals,
each for one plain change. At the ceiling: Manual, because a container that
needs that much local disk should be writing to a volume.

### 12. Pods refused at creation

**What happens.** The controller asks the API server for a pod and is
refused. No pod exists, so nothing shows as crashing; the workload simply
has fewer pods than it wants, or a rollout never finishes.

**Common causes.** The pod names a ServiceAccount that does not exist; the
spec violates the namespace's Pod Security level; resources fall outside a
LimitRange; the ResourceQuota is used up; an admission webhook rejects it.

**Detection.** A ReplicaSet, StatefulSet or DaemonSet with fewer pods than
it wants and a `FailedCreate` event in the last 20 minutes. The event
message is classified into the causes above. This is the only detector that
does not start from a pod.

**Fix.** `rollback-workload`, when it is the latest revision that is refused
and the earlier one is known to be acceptable: its pods are still running,
or (for a missing ServiceAccount) it names a different account. A full
quota is always Manual: abandoning the rollout would clear the symptom, but
the decision is about capacity. kbark never creates ServiceAccounts, loosens
a security level or edits a quota.

### 13. Node not healthy

**What happens.** A node stops reporting (`NotReady`) or runs short of
memory, disk or process IDs. Its pods stop being run or start being evicted.

**Common causes.** The machine or its kubelet is down; the node lost its
network; a disk filled up; too many pods without limits.

**Detection.** The node's `Ready` condition has been false or unknown for
over a minute, or a pressure condition is true. A node has no namespace, so
this incident has none. Evidence: the condition, its message, how long it
has lasted, and how many pods are assigned to the node.

**Fix.** None. kbark cannot repair a machine, and Kubernetes already keeps
new pods off such a node and evicts the old ones. What kbark adds is the
incident, the explanation, and the follow-on incidents below: pods the node
leaves stuck (family 14), dead records (family 9) and pods over their own
limits (families 1 and 11) each get their own fix.

### 14. Pod stuck terminating

**What happens.** A pod was deleted and stays in `Terminating`. Its
controller counts it as present, so a Deployment can sit one replica short
indefinitely.

**Common causes.** The pod's node is down, so no kubelet can confirm the
containers stopped; a finalizer whose controller never finishes; a process
that ignores the stop signal under a long grace period.

**Detection.** A pod more than two minutes past its deletion deadline (the
deadline already includes the pod's own grace period). The cause is refined
from what else is known: a finalizer on the pod, or a node that is not
Ready or no longer exists.

**Fix.** `force-delete-pod`, in exactly one case: the node is down, the pod
has no finalizer, and it does not belong to a StatefulSet. The record is
deleted with no grace period and a UID precondition, and the controller
starts a replacement on a healthy node. The proposal is medium risk and
warns that a node which is only cut off, not off, may still be running the
old container. In every other case the incident is Manual, with the reason:
a finalizer is someone's request for cleanup and is not bypassed; on a live
node the kubelet is still stopping the container; and a StatefulSet pod
forced off an unreachable node can leave two pods with one identity.

### 15. Failed Job

**What happens.** A Job's pods fail until it runs out of retries
(`BackoffLimitExceeded`) or time (`DeadlineExceeded`), and the Job stops.

**Common causes.** A bug or a missing dependency in the task; a deadline
shorter than the work; the same causes as families 1 to 4, inside a Job.

**Detection.** A Job whose `Failed` condition became true within the last
hour. A Job created by a CronJob is reported against the CronJob, and not at
all once a later run of that CronJob has succeeded.

**Fix.** None: a Job's pod template cannot be changed after creation, and
kbark does not create objects. The explanation is built from the failed
pod's log. For a CronJob the next scheduled run uses the CronJob's current
template, so a correction there takes effect by itself.

## Fixing at the source: pull requests

A live change to a workload that Argo CD, Flux or a pipeline deploys from
Git is drift: the next sync may undo it, and the repository no longer says
what is running. When a workload declares where its manifest lives
(`kbark.dev/git-repo` and `kbark.dev/git-path` annotations) and kbark has
been given a token for the Git host, a proposal gains a second option: open
a pull request with the same change against that file. Nothing is written
to the cluster. The incident waits, records the merge, and closes as
verified when the failure clears; a pull request closed without merging
puts the proposal back in front of a person. This covers the fixes that are
an edit to a manifest (limits, probe timing, pull policy, Service selector).
A rollback is not one: the previous revision is in the repository's
history, not in a file.

## Standing approvals

By default every fix needs a person each time. Where policy sets
`standing_approvals: low-risk`, an approver may tick "apply this
automatically next time" while approving a low-risk fix. From then on kbark
applies that action type to that workload without a click, recorded under
the approver's name. It is one cluster, one namespace, one workload and one
action type; it lasts 30 days; anyone who may approve may revoke it; and it
is revoked automatically the first time a fix applied under it fails
verification or cannot be applied. The limit on fixes per workload per hour
still applies. Medium-risk fixes and workloads with a Git source are never
covered.

## Why "roll back" appears so often

Most production failures follow a change. When the failing pods all belong
to the latest revision of a Deployment and an earlier revision exists,
returning to it restores the last configuration before the failure without
kbark needing to understand the application. It is also exactly reversible.
kbark therefore prefers rollback whenever revision history supports it, and
declines to invent a forward fix when it does not. kbark does not know that
the earlier revision was healthy; verification after the rollback is what
establishes that.

A StatefulSet needs one more step. With its default ordered pod management
it waits for a broken pod to become ready before touching it again, so
reverting the template leaves the broken pod in place indefinitely (the
Kubernetes documentation calls this a forced rollback and tells the operator
to delete the pod). kbark's StatefulSet rollback therefore lists the failing
pods in the proposal and deletes them, by UID, right after the template is
restored. Their volumes are untouched.

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
| `selector`, before the selector fix existed | kbark-dev | Service selector matches no pod | None (Manual) | Left open | — |
| `oom`, kbark running in-cluster | kbark-staging | Out of memory (inferred) | Memory limit 64Mi to 128Mi | Verified | 38s |
| `cordon`, kbark running in-cluster | kbark-staging | Scheduling, cordoned node | Uncordon node | Verified | 17s |
| `evicted`, kbark running in-cluster | kbark-staging | Evicted pod | Delete dead pod | Verified | 38s |
| `crash`, kbark on the server (demo.kbark.dev host) | kbark-dev | Application crash | Roll back revision 2 to 1 | Verified | 20s |
| `oom`, kbark on the server | kbark-staging | Out of memory (inferred) | Memory limit 64Mi to 128Mi | Verified | 39s |
| `bad-image`, kbark on the server | kbark-staging | Image pull, bad reference | None: the server's policy forbids rollback on staging | Manual, with the policy named | — |
| `selector` | kbark-dev (laptop) | Service selector matches no pod | Selector `app=check-out` to `app=checkout` | Verified; then rolled back, detected again and fixed again | 1m37s |
| `never-pull` | kbark-dev (laptop) | Image pull, policy `Never` | Pull policy `Never` to `IfNotPresent` | Verified | 1m48s |
| `disk` | kbark-dev (laptop) | Local disk limit | Ephemeral-storage limit 16Mi to 32Mi | Verified; a second incident then proposed deleting the 2 dead records, verified | 1m14s |
| `slow-ready` | kbark-dev (laptop) | Readiness probe, timeout | Readiness timeout 1s to 5s | Verified | 59s |
| `no-account` | kbark-dev (laptop) | Pods refused: ServiceAccount missing | Roll back revision 2 to 1 | Verified | 1m38s |
| `sts-crash` | kbark-dev (laptop) | Application crash on a StatefulSet | Roll back revision 2 to 1 and replace the broken pod | Verified | 1m19s |
| `stuck-pod` | kbark-dev (laptop) | Application crash, one pod of three | Replace the pod | Verified | 26s |
| The same seven scenarios, kbark on the server with its restricted service account | kbark-dev | As above | As above | All verified; the `disk` follow-up deleted 49 dead records | Not comparable: approval was delayed about 28 minutes |
| `ds-crash` | kbark-k3s (laptop, three nodes) | Application crash on a DaemonSet | Roll back revision 2 to 1 | Verified | 1m18s |
| `oom` with "apply automatically next time" ticked, then `oom` again | kbark-k3s (laptop) | Out of memory (inferred) | Memory limit 64Mi to 128Mi, the second time with no click | Both verified; the second 18s after detection | 18s |
| `node-down` | kbark-k3s (laptop) | Node not Ready; then pod stuck terminating on it | Node: none (Manual). Pod: force-delete | Pod fix verified; replacement ran on another node. Node incident closed when the node was removed | 32s (pod) |
| `failed-job` | kbark-k3s (laptop) | Failed Job, backoff limit | None (Manual), explained from the pod's log | Left open | — |
| `stuck-finalizer` | kbark-k3s (laptop) | Pod stuck terminating, finalizer | None (Manual): the finalizer is named and not bypassed | Left open | — |

The recovery times in the laptop rows include about a minute between
detection and approval, because all proposals were approved together.

Two older bare pods in the `default` namespace of kbark-dev (`cause-crash`,
`bad-image`, the v0.1 demo fixtures) were also detected and correctly marked
Manual, because a pod without a controller cannot be fixed in place.

No approved fix failed verification in these runs. The failed-verification
path is covered by an automated test (`TestFixThatDoesNotHelpIsMarkedFailed`).
