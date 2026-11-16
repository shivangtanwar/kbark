# Roadmap

kbark is a Kubernetes terminal UI in the spirit of k9s, with one
new key: **`?`** opens an inline AI diagnosis. The v0.1 line is
the first publicly tagged release.

## What v0.1 ships

- Live resource tables for pods, deployments, statefulsets,
  daemonsets, services, jobs, cronjobs, nodes, events, configmaps,
  secrets, and ingresses
- Read-only describe + YAML modal on `Enter` (managed-fields stripped)
- Tool-using AI diagnosis on **`?`** against Anthropic, OpenAI, or
  Ollama — the model can pull additional logs, events, and previous
  container output mid-session
- Line-local secret redaction before any byte leaves for an AI
  provider, with no-leak tests pinning the contract
- Per-diagnosis Markdown transcripts in `~/.cache/kbark/diagnoses/`
- Profile system with mid-session switch (`:profile <name>`)
- Cosign-signed releases via GitHub OIDC; install paths for
  Homebrew, Scoop, and `go install`
- envtest E2E coverage of the kube layer

## Autofix operations layer (in development on `main`)

- `kbark serve`: watches every pod across several kubeconfig contexts
  (or in-cluster) and opens an incident for each detected failure
- Deterministic detectors for out-of-memory kills, crashes, image pull
  failures, missing configuration, probe failures, scheduling,
  storage, evictions, local disk limits, Service selector mismatches
  and pods refused at creation
- Nine typed fixes (raise a memory or ephemeral-storage limit, roll
  back a Deployment, StatefulSet or DaemonSet, relax probe timing,
  allow an image pull, correct a Service selector, delete a dead pod,
  replace an unhealthy pod, uncordon a node), each previewed as a
  dry-run diff and applied only after approval
- Verification after every fix, exact rollback, an attempt limit and
  an append-only audit log
- Web console (React, TypeScript) with sign-in and roles, health and
  incident trends, and a `kbark incidents` terminal client
- Policy file, saved incident history, webhook notifications
- Deployment recipes: local, in-cluster, and a single server behind
  nginx

See [docs/AUTOFIX_PLAN.md](docs/AUTOFIX_PLAN.md) and
[docs/FAILURE_PLAYBOOK.md](docs/FAILURE_PLAYBOOK.md).

## What's coming next

These are the directions the project is heading. **None are
promised, none are dated.** Open an issue to discuss any of them.

- **Log viewer with diagnose** — `?` on a log line that pulls
  the surrounding window (not just the line) into the diagnosis
- **Single sign-on** — accounts are a file today; OIDC is the next step
- **Incident screen in the terminal UI** — today the terminal client
  is the `kbark incidents` command
- **Confirmed out-of-memory kills by default** — the node-metrics check
  exists behind `--confirm-oom` because it needs a broad permission
- **More providers** — Azure OpenAI, Amazon Bedrock, Google Vertex
- **Per-namespace defaults** — pin a profile to a namespace
  via config
- **Krew distribution** — once kbark has enough adoption that
  shipping as a kubectl plugin doesn't undermine the "the new k9s"
  framing

## What kbark deliberately won't do

- **Background telemetry.** Never. Your kubeconfig, cluster
  state, and AI prompts stay on your machine
- **Unattended fixes.** Every change kbark makes is approved by a
  named person first
- **Free-form remediation.** No shell, no exec, no model-written
  commands. The set of fixes is a short typed list
- **A managed service.** kbark is a CLI, not a SaaS

## How decisions get made

kbark is a single-maintainer project. Feature proposals land in
GitHub Issues — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
proposal template. Items in "What's coming next" are open for
discussion; items in "What kbark deliberately won't do" are
closed-by-default. Challenge any of these calls in an issue.
