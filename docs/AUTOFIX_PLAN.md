# kbark PRJ-4: Autofix Operations Layer — Scope, Design, Deployment and Demonstration

Date: 16 November 2026. Demonstration: 17 November 2026.

Status: built, exercised on two Kind clusters locally, and deployed on a
server (shebang-ParentHost) for https://demo.kbark.dev. Section 8 lists
what was verified and what was not.

## 1. What changed

Until now kbark has been an analysis tool: a person opens the terminal UI,
selects a failing resource, presses `?`, and reads an AI-written explanation.
The person then has to work out the fix and apply it with `kubectl`.

The new scope makes kbark an operations tool:

| | Before (v0.1) | Now (autofix layer) |
|---|---|---|
| Trigger | A person presses `?` on one resource | kbark watches every pod continuously |
| Detection | The language model reads logs and guesses | Deterministic detectors in Go code |
| Output | Free-form explanation | An incident with a typed, previewable fix |
| Action | None; kbark is read-only | kbark applies the fix after a person approves it |
| Follow-up | None | kbark verifies recovery and can roll the fix back |
| Interface | Terminal UI | Web console with sign-in and roles; the terminal UI and a CLI client remain |
| Reach | One cluster context | Several kubeconfig contexts at once |
| Role of AI | Source of the diagnosis | Explains the evidence to the approver; never chooses or runs an action |

The human-in-the-loop rule is absolute: kbark never writes to a cluster
without a named person approving that specific fix. In the code, the only
caller of `remediate.Apply` is `Engine.Approve`, which refuses an empty
operator name; a test (`TestNothingIsWrittenWithoutApproval`) asserts that no
write reaches the API before approval.

## 2. Architecture

```
 kubeconfig contexts (kind-kbark-dev, kind-kbark-staging, ...)
        |  one clientset + informers per context
        v
 +-------------+  cached state  +------------+  findings  +----------------+
 | fleet       | -------------> | detect     | ---------> | incident store |
 | (watchers)  |                | (pure Go)  |            | (dedup, state) |
 +-------------+                +------------+            +----------------+
                                                             |          |
                                           proposal          |          | explanation
                                 +-----------------+ <-------+          +--> AI provider
                                 | remediate       |                        (redacted evidence)
                                 | playbook -> fix |
                                 +-----------------+
                                          | dry-run diff
                                          v
                       +--------------------------------------+
                       | web dashboard (HTTP + live updates)  |
                       |  clusters | pods | incidents | audit |
                       |  [Approve] [Reject] [Roll back]      |
                       +--------------------------------------+
                                          | approve (named operator)
                                          v
                       precondition check -> apply -> verify -> audit log
```

### Incident lifecycle

```
Detected -> Proposed -> Applying -> Verified
               |           |    \-> Failed -> Rolled back
               |           \-> (target changed) -> Proposed again
               \-> Rejected
Detected -> Manual     (no safe automatic fix exists; kbark explains and stops)
Proposed / Manual / Rejected / Failed -> Resolved (the failure cleared on its own)
```

An incident is keyed by cluster, namespace, owning workload and failure
family, so ten crashing pods of one Deployment are one incident. A failure
must be absent for 45 seconds before its incident resolves, because a
crash-looping pod looks healthy for a few seconds on every restart.

### Components

| Package | Responsibility |
|---|---|
| `internal/detect` | Pure functions from pod state and events to findings. No network, no clock, no AI |
| `internal/fleet` | One clientset and informer set per cluster; owner resolution; cluster and pod summaries |
| `internal/incident` | Incident record, in-memory store with deduplication and hysteresis, append-only audit log |
| `internal/remediate` | The whole write path: plan, dry-run, apply, roll back, settle. Five typed actions |
| `internal/autofix` | The loop: scan, plan, approve, verify. Also builds the redacted AI payload |
| `internal/policy` | Per-cluster and per-namespace rules: detect-only areas, allowed fix types, memory ceiling |
| `internal/auth` | Accounts with bcrypt hashes, roles (viewer, approver, admin), sessions, sign-in lockout |
| `internal/notify` | Webhook notifications for incident events |
| `internal/server` | HTTP API, sign-in, server-sent-events change stream, embedded console |
| `web/` | The console: React, TypeScript, Vite, Tailwind, Recharts. Built into `internal/server/dist` |
| `cmd/kbark/serve.go` | `kbark serve` |
| `cmd/kbark/incidents.go` | `kbark incidents`: terminal client for the same API |
| `cmd/kbark/users.go` | `kbark users hash`: creates a users-file entry |
| `internal/diagnose`, `ai`, `redact` | Reused unchanged for explanations |
| `internal/tui` | Unchanged. The `?` diagnosis still works |

### The allowlisted actions

kbark can perform only these nine writes. Each is a typed struct, not a command
string, so nothing a language model says can become an action.

| Action | API call | Used for | Reversible by |
|---|---|---|---|
| `set-memory-limit` | update workload pod template | out-of-memory kills | restoring the saved template |
| `rollback-workload` | update the workload's template to its previous revision (ReplicaSets for Deployments, ControllerRevisions for StatefulSets and DaemonSets) | failures on the latest rollout | restoring the saved template |
| `restart-pod` | delete pod (UID precondition) | one pod failing while siblings on the same revision have been ready for 5 minutes | not reversible; the controller starts a replacement |
| `tune-probe` | update probe timing in the pod template | liveness kills on slow start | restoring the saved template |
| `delete-pod` | delete pod (UID precondition) | evicted pods left behind | not reversible; the pod was already dead |
| `uncordon-node` | patch `spec.unschedulable=false` | pods pending on a cordoned node | cordoning again |
| `set-ephemeral-storage-limit` | update workload pod template | pods evicted for exceeding their local disk limit | restoring the saved template |
| `set-image-pull-policy` | update workload pod template (`Never` to `IfNotPresent`) | an image absent from the node that the kubelet may not pull | restoring the saved template |
| `fix-service-selector` | update the Service's selector | a Service whose selector nearly matches the pods of exactly one workload | restoring the saved selector |

`tune-probe` also covers a readiness probe that times out (it raises only
the timeout), and `rollback-workload` also covers pods that the API server
refuses to create on the latest revision. A StatefulSet rollback deletes the
broken pods after restoring the template, because a StatefulSet does not
replace them by itself.

### Guardrails

1. Fixes target the owning workload, not the pod. A pod patch would be
   discarded when the controller replaces the pod.
2. Every proposal is sent to the API server as a dry run first; the dashboard
   shows the resulting diff of the pod template.
3. Approval is bound to the target's UID and `metadata.generation`. If the
   spec changed since the proposal was shown, the apply is refused and the
   proposal is recomputed.
4. The pre-fix pod template is stored with the incident, so a rollback is
   exact. Rollback is refused if someone changed the workload after the fix.
5. After applying, kbark waits for the rollout to finish and for the failure
   to stay clear (15 seconds by default; a relaxed probe must stay clear for
   its new initial delay plus 30 seconds). If that does not happen within the
   verification window (150 seconds by default), the incident is marked Failed
   and rollback is offered.
6. Limits, set by policy (`--policy`, with built-in defaults): memory at most
   doubles per fix and never exceeds the ceiling (default 2Gi); at most two
   applied fixes per workload per hour; system namespaces are detect-only;
   a rule can restrict which fix types are proposed for a cluster or
   namespace. Policy can only narrow what kbark offers. It cannot add fix
   types or remove the approval step.
7. Workloads managed by Helm, Argo CD or Flux carry a warning that a live
   change will drift from its source.
8. Evidence sent to the AI provider goes through `internal/redact`, and cluster
   text is wrapped in `<cluster-data>` tags the prompt marks as untrusted.
9. Every event is written to the audit log with time, operator, cluster,
   workload, action and diff.
10. The console API refuses requests with a foreign `Host` or `Origin` and
    writes that are not JSON, so another web page cannot approve a fix.
11. With sign-in enabled, the name recorded against an approval is the
    signed-in account's, viewers cannot change anything, and kbark refuses to
    listen on a non-loopback address without sign-in.

## 3. What was reworked in the existing project

| Item | Change |
|---|---|
| README | Rewritten around detect, propose, approve, apply, verify; the `?` flow kept as a second mode |
| ROADMAP | Multi-cluster and action mode moved into shipped scope |
| `kube.AllowedVerbs` | Still the read-only set for the terminal UI; comment now points to `internal/remediate` for writes |
| Diagnose prompts | Untouched for `?`. A new prompt (`autofix.ExplainPrompt`) frames the model as adviser to the approver |
| SECURITY.md | Write path, approval model and dashboard exposure added |
| Proposal evaluation (150 scored outputs) | Replaced by the fix-outcome table in [FAILURE_PLAYBOOK.md](FAILURE_PLAYBOOK.md) |
| Proposal's EvidenceBundle, evidence IDs, three-variant ablation | Not built. Detectors record evidence in a simpler label/value form |

The proposal document and the mid-semester slides still describe the old
scope and need to be updated by hand.

## 4. Development: what was built

- Detectors for eleven failure families (nine from pod state, plus Services
  without endpoints and pods refused at creation), table-driven tests.
- Multi-cluster watchers over kubeconfig contexts, plus an in-cluster mode.
- Incident store, audit log, and the scan/plan/approve/verify engine.
- All nine actions, each with plan, dry-run, apply and (where meaningful)
  rollback.
- The engine plans a "no automatic fix" incident again every 30 seconds
  while the failure is observed, and asks again for an explanation that
  was unavailable, so neither is stuck with a first answer that has
  stopped being true.
- Web console in React and TypeScript, dark and light themes: overview with
  KPI tiles and health and incident trends, incidents, incident detail with
  evidence, explanation, diff and an approval dialog, clusters, pods, audit
  log, settings.
- Sign-in with roles, and `kbark users hash` to create accounts.
- Policy file, saved incident state and trend history, webhook notifications.
- `kbark incidents` terminal client: list, show, approve, reject, rollback.
- AI explanation through the existing providers or any OpenAI-compatible
  endpoint (`base_url` and `api_key_env` in a profile); optional.
- Demo environment: `demo/demo.sh` with fifteen scenarios.
- Deployment files: `Dockerfile`, `deploy/rbac.yaml`, `deploy/kbark.yaml`.

Not built: an incident screen inside the terminal UI (the CLI client covers
terminal use), single sign-on, automatic fixes for storage, network,
credentials and application bugs.

## 5. Deployment

### Local process (used for the demonstration)

`kbark serve` runs on a laptop and reaches the clusters through kubeconfig
contexts. Nothing is installed in the clusters. The dashboard listens on
`127.0.0.1:8765`.

```sh
demo/demo.sh up        # two Kind clusters with a healthy baseline
go build -o bin/kbark ./cmd/kbark
bin/kbark serve --contexts kind-kbark-dev,kind-kbark-staging
```

Useful flags: `--addr`, `--auth-file`, `--policy`, `--state`, `--audit-log`,
`--public-url`, `--notify-webhook`, `--verify-timeout`, `--verify-hold`,
`--confirm-oom`, `--no-ai`, `--profile` (which AI provider and model).

### Server (https://demo.kbark.dev)

`deploy/server/` puts the console on one Linux server: two Kind clusters in
Docker, kbark in a locked-down container on `127.0.0.1:8771` using a
service-account token bound to `deploy/rbac.yaml` (not cluster-admin), nginx
and Let's Encrypt in front. `setup.sh` needs no root; `install-nginx.sh`
does. See [deploy/server/README.md](../deploy/server/README.md).

### In-cluster

```sh
docker build -t kbark:dev .
kind load docker-image kbark:dev --name kbark-staging
kubectl apply -f deploy/rbac.yaml -f deploy/kbark.yaml
kubectl -n kbark-system port-forward svc/kbark 8765:8765
```

kbark then watches the cluster it runs in under its own service account.

### Permissions

`deploy/rbac.yaml` is the complete permission set:

| Verbs | Resources |
|---|---|
| get, list, watch | pods, events, nodes, services, deployments, statefulsets, daemonsets, replicasets, controllerrevisions |
| get | pods/log |
| update | deployments, statefulsets, daemonsets |
| patch | nodes |
| delete | pods |

No access to Secrets or ConfigMaps, no create, no exec. Kind's default
kubeconfig is cluster-admin, which is more than this; for local mode on a
real cluster, bind the same ClusterRole to the kubeconfig user.

## 6. Demonstration

The quickest way to run any scenario is the console's **Demo lab** page
(start kbark with `--demo`; on the server it is already on for the admin
account): choose the cluster, click "Inject failure", and the card links to
the incident once kbark opens it. "Reset namespace" restores the baseline.
The commands below do the same from a terminal.

Before the session: `demo/demo.sh up`, then `demo/demo.sh reset` on both
contexts, start `kbark serve`, open the console and sign in (or, in local
open mode, type your name under "Signing as"). Pre-inject the scenarios
marked (pre) about two minutes early. On the server the same scenarios are
run with `~/kbark/deploy/demo break <scenario> kind-kbark-dev`; note that the
server's policy forbids rollbacks on `kbark-staging`, which is itself worth
showing.

| Time | Step | Command | What the audience sees |
|---|---|---|---|
| 0:00 | Context | | One slide: v0.1 explained failures; now kbark fixes them with approval |
| 0:30 | Overview | | Two clusters, pod health bars, the pods table |
| 1:00 | Live failure | `demo/demo.sh break oom kind-kbark-staging` | Within about 5 to 10 seconds an incident opens by itself; the staging bar turns red |
| 1:40 | Incident detail | | Evidence from the API, the proposed 64Mi to 128Mi change as a diff, the inference warning |
| 2:10 | Approve | click Approve and apply | Applying, then Verified; the pod turns healthy; "recovered Ns after detection" |
| 2:50 | Bad rollout (pre) | `demo/demo.sh break crash` | Proposal is a rollback to the previous revision; approve; verified |
| 3:30 | Human control (pre) | `demo/demo.sh break bad-image` | Reject the rollback with a reason; nothing changes in the cluster |
| 3:50 | kbark refuses to guess (pre) | `demo/demo.sh break missing-config` | "No automatic fix", naming the missing ConfigMap. Then `demo/demo.sh break fix-config`: the incident closes itself |
| 4:20 | Rollback and audit | click Roll back on the memory fix | Audit log tab: who approved what, with the diff |
| 4:50 | Close | | Fix-outcome table; limits |

Other scenarios available:

| Scenario | Failure | Proposed fix |
|---|---|---|
| `slow-start` | liveness probe kills a slow starter | relax probe timing |
| `slow-ready` | readiness check slower than its deadline | raise the readiness timeout |
| `cordon` | pods pending on a cordoned node | uncordon the node |
| `evicted` | a dead pod record | delete it |
| `selector` | Service selector typo | correct the selector |
| `never-pull` | image absent, pull policy `Never` | allow the pull |
| `disk` | eviction for exceeding the local disk limit | raise the limit, then a second proposal deletes the dead records |
| `no-account` | rollout names a ServiceAccount that does not exist; no pod is ever created | roll back |
| `sts-crash` | crashing StatefulSet rollout | roll back and replace the broken pod |
| `stuck-pod` | one of three pods has corrupt local state | replace that pod (needs the `sessions` pods to be five minutes old) |

`selector`, `no-account` and `sts-crash` propose medium-risk changes, which
the server's policy does not allow on `kbark-staging`; run them on
`kbark-dev`. For a local run with explanations, put `KBARK_AI_API_KEY` in
`~/.config/kbark/kbark.env` and start `task serve -- --profile codex` (the
profile points at the same OpenAI-compatible endpoint the server uses).

Fallbacks: detection and fixes do not need the AI provider, so a provider
outage changes nothing but the explanation panel. If one cluster fails, run
every scenario on the other. Screenshots of a working run are in
`docs/screenshots/`.

Kind preloading: `demo/demo.sh up` tries to preload images into the nodes
and falls back to letting the nodes pull them, so the first run needs a
network connection.

## 7. Risks and limits

| Risk or limit | Position |
|---|---|
| A fix makes things worse | Dry run, precondition, verification, exact rollback, attempt limit |
| Rollback proposed when the previous revision was also broken | Proposed only when the failing pods are on the latest revision and an earlier one exists; verification catches the rest |
| Out-of-memory cause is inferred on some runtimes | Labelled as inferred, with a warning on the proposal (see the playbook) |
| Accounts are a file | bcrypt hashes, roles and lockout; no single sign-on or second factor yet |
| Sessions are in memory | A restart signs everyone out; incidents, trends and the audit log are kept |
| One process, no leader election | Run a single instance per cluster |

## 8. Verification record (16 November 2026)

Verified by running:

- `go test -race ./...` passes: detectors, remediation (including StatefulSet
  rollback and pod replacement), engine (approval, verification, restart
  survival, policy), auth, notifications, server (sign-in, roles, guards).
- Locally, on two Kind clusters: every original action type applied and
  verified at least once (see the fix-outcome table), rollback of an applied
  fix, rejection, self-resolution of a manual incident.
- The console in a real browser (headless Chrome driven over the DevTools
  protocol): sign in on the form, open an incident, click Approve, confirm in
  the dialog, and watch it move to verifying; every page in both themes with
  no script errors.
- Sign-in and roles through the CLI and the API: a viewer is refused,
  approvals are recorded under the account name, lockout answers 429.
- Persistence: six incidents restored with the same IDs after a restart, and
  a fix applied before the restart rolled back after it (automated test).
- Notifications: every event delivered to a local webhook receiver.
- In-cluster mode under `deploy/rbac.yaml`.
- On the server: both clusters healthy; kbark's service account can update
  workloads, delete pods and patch nodes, and cannot read secrets, create
  anything or exec; two fixes applied and verified; a policy rule blocked a
  rollback on the staging cluster with an explanation; the viewer account is
  read-only; state survived a container restart.

- The public address, https://demo.kbark.dev: valid certificate, HTTP
  redirects to HTTPS, the API answers 401 without a session, the live stream
  passes through nginx, and a rollback was approved in a browser over the
  public address and verified 31 seconds after detection.
- AI explanations against a real provider (an OpenAI-compatible endpoint,
  model `gpt-6.1-sol`) on the server: explanations quote the evidence and the
  container log. One explanation misstated the memory request; the model is
  now given the exact diff, and a later explanation of the same fix stated
  correctly that the limit and the request both go from 64Mi to 128Mi.
- Automatic re-planning: an incident left as "no automatic fix" because its
  Deployment was briefly missing returned to a proposal by itself.

- The five recoveries added last, on the laptop's Kind cluster and again on
  the server under the restricted service account: pull policy, local disk
  limit followed by deletion of the dead records, Service selector (applied,
  rolled back, detected again, applied again), readiness timeout, and
  rollback of a rollout whose pods were refused for a missing
  ServiceAccount. All verified.
- `restart-pod` and StatefulSet rollback on a live cluster, both places:
  one pod of three replaced, and `ledger-1` recreated on revision 1.
- A local run with AI explanations through the `codex` profile.

Not verified:

- `--confirm-oom`: the metric parser is tested, but the Kind runtime never
  reports out-of-memory events, so a confirmation has not been observed.
- DaemonSet rollback on a live cluster (automated test against a fake
  cluster only).
- Refused pod creation for causes other than a missing ServiceAccount (Pod
  Security, LimitRange, quota, webhook): classification is tested from
  message text, not produced on a cluster.
- A Service selector fix where two workloads nearly match (automated test
  only).
- `golangci-lint`: the installed binary could not type-check this Go version.
  `go vet` and `gofmt` are clean.
- The multi-stage `Dockerfile`.
- Any cluster other than Kind.
