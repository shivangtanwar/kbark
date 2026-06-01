# kbark

**A Kubernetes terminal UI in the spirit of k9s — with one new key.**

Press **`?`** on any pod, deployment, log line, or event, and kbark pulls
the context for you, streams a diagnosis inline, and tells you the cause
in plain English. Read-only. Single binary. Bring your own key
(Anthropic, OpenAI, or Ollama).

![kbark demo](docs/demo.gif)

## Install

```sh
# Homebrew (macOS, Linux)
brew install shivangtanwar/tap/kbark

# Scoop (Windows)
scoop bucket add kbark https://github.com/shivangtanwar/scoop-bucket
scoop install kbark

# Go (any platform)
go install github.com/shivangtanwar/kbark/cmd/kbark@latest
```

Pre-built archives (Linux / macOS / Windows, amd64 + arm64) and a
cosign-signed `SHA256SUMS` are published with every release on the
[Releases page](https://github.com/shivangtanwar/kbark/releases).

## Quick start

```sh
export ANTHROPIC_API_KEY=sk-ant-...   # or OPENAI_API_KEY, or run Ollama
kbark                                  # uses your current kubeconfig context
```

Arrow to any resource and press **`?`** to ask kbark what's wrong with
it. Press `q` (or `Ctrl-C`) to quit.

Sanity-check your setup any time with `kbark doctor`.

## Configuration

kbark reads `~/.config/kbark/config.yaml` (or the platform equivalent).
A config file is optional — the built-in default profile points at
Anthropic with `claude-sonnet-4-6` and saves transcripts under
`~/.cache/kbark/diagnoses/`.

```yaml
default_profile: dev

profiles:
  dev:
    provider: anthropic
    model: claude-sonnet-4-6
    transcripts: on
    token_budget: 0          # 0 = unbounded; set a cap to fail-fast on huge payloads

  cheap:
    provider: openai
    model: gpt-4o-mini

  offline:
    provider: ollama
    model: llama3.2
```

Switch profiles at startup with `kbark --profile cheap`, mid-session
with `:profile cheap`, or via the `KBARK_PROFILE` env var.

## What kbark does

- **Live resource tables** for pods, deployments, statefulsets,
  daemonsets, services, jobs, cronjobs, nodes, events, configmaps,
  secrets, and ingresses — uniform keymap, real-time updates.
- **Read-only describe + YAML modal** on `Enter` (managed-fields
  stripped so you can actually read the YAML).
- **AI diagnosis on `?`** that pulls describe + events + recent logs,
  redacts secrets and obvious credentials before any byte leaves the
  process, and streams a plain-English explanation inline.
- **Tool-using diagnosis sessions** — the model can ask for more
  logs, more events, or the previous container's output without you
  reaching for `kubectl`.
- **Per-diagnosis transcripts** saved as Markdown under
  `~/.cache/kbark/diagnoses/` so you can grep for "the time I saw
  CrashLoopBackOff on the canary" months later.
- **Three providers**: Anthropic, OpenAI, Ollama. Models swap with a
  one-line config edit.

## What kbark deliberately doesn't do

- **No writes.** kbark cannot delete, scale, restart, exec, or patch
  anything. The `?` key never produces a `kubectl` command for you
  to run — it produces an explanation.
- **No background telemetry.** Your kubeconfig, API key, and cluster
  state stay on your machine, except for the redacted payloads you
  send to your chosen AI provider on the `?` key.
- **No multi-cluster federation.** kbark is one terminal, one
  cluster, one context. Switch contexts via `--context` or
  `kubectl config use-context` like usual.

## Security

- Every payload bound for an AI provider passes through `internal/redact`
  first — credentials, tokens, passwords, and JWT-shaped values are
  replaced with `<redacted>` before the network call.
- Releases ship a cosign-signed `SHA256SUMS`; verify with the recipe in
  [docs/RELEASING.md](docs/RELEASING.md).
- Report a vulnerability via the
  [security advisory form](https://github.com/shivangtanwar/kbark/security/advisories/new)
  — not a public issue. See [SECURITY.md](SECURITY.md).

## Status

kbark is in active development. The v1 wedge is intentionally narrow:
a k9s-shaped TUI plus the `?` key. Bug reports and feature proposals
welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) and the issue
templates.

## License

[Apache-2.0](LICENSE).
