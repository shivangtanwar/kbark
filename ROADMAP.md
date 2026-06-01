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

## What's coming next

These are the directions the project is heading. **None are
promised, none are dated.** Open an issue to discuss any of them.

- **Log viewer with diagnose** — `?` on a log line that pulls
  the surrounding window (not just the line) into the diagnosis
- **Action mode** — restart, scale, exec, etc., behind explicit
  per-action confirmation. Read-only is the v1 contract; writes
  get their own trust model
- **More providers** — Azure OpenAI, Amazon Bedrock, Google Vertex
- **Per-namespace defaults** — pin a profile to a namespace
  via config
- **Krew distribution** — once kbark has enough adoption that
  shipping as a kubectl plugin doesn't undermine the "the new k9s"
  framing

## What kbark deliberately won't do

- **Background telemetry.** Never. Your kubeconfig, cluster
  state, and AI prompts stay on your machine
- **Multi-cluster federation.** kbark is one terminal, one
  cluster, one context. Switch contexts via `--context` or
  `kubectl config use-context`
- **A managed service.** kbark is a CLI, not a SaaS

## How decisions get made

kbark is a single-maintainer project. Feature proposals land in
GitHub Issues — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
proposal template. Items in "What's coming next" are open for
discussion; items in "What kbark deliberately won't do" are
closed-by-default. Challenge any of these calls in an issue.
