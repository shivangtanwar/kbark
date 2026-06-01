# Contributing to kbark

Thanks for considering a contribution. kbark is a single-maintainer project right now, so the bar for merge is high and the loop is fast.

## Before you open a PR

- **Open an issue first** for anything larger than a typo or a one-file fix. Unscoped PRs will be closed without review.
- Check `ROADMAP.md` — if your idea isn't on it and isn't a bug fix, the answer may be "not yet."
- v1 is scoped tight on purpose. New feature ideas land in `ROADMAP.md` only after discussion.

## Repo layout

| Path | What |
|---|---|
| `cmd/kbark/` | CLI entry — cobra subcommands (`doctor`, `transcripts`, `version`) |
| `internal/tui/` | Bubbletea UI: views, components, message loop, theme |
| `internal/kube/` | client-go wrappers — informer factory, generic resource lister/service, log streamer |
| `internal/kube/kinds/` | per-kind Plugin definitions (one file per resource kind) |
| `internal/diagnose/` | AI session orchestration: context builders, system prompts, tool dispatcher |
| `internal/describe/` | kubectl-style describe + YAML serialisation for the Enter modal |
| `internal/config/` | YAML config loader, profile resolution |
| `internal/redact/` | secret scrubber used by every text leaving for the AI |
| `internal/tokens/` | rough chars/4 token estimator for the per-session budget |
| `internal/transcript/` | per-diagnosis markdown writer to `~/.cache/kbark/diagnoses/` |
| `internal/doctor/` | `kbark doctor` health checks |
| `internal/ai/` | provider-agnostic AI streaming interface + Anthropic/OpenAI/Ollama impls |
| `docs/` | RELEASING.md, the VHS demo `.tape`, the rendered `demo.gif` |
| `.github/workflows/` | CI (3-OS build + CodeQL) and release (goreleaser on `v*` tags) |

## Build, test, lint

```sh
task build        # builds ./bin/kbark
task test         # go test ./...
task lint         # golangci-lint run
task run          # go run ./cmd/kbark
```

Pre-commit hooks live in `lefthook.yml`. Install once with `lefthook install` after cloning.

## Dev cluster fixtures

The canonical local cluster for development is a single-node kind setup with three test pods that exercise the most-used `?` paths. Recreate after `kind delete cluster --name kbark-dev`:

```sh
kind create cluster --name kbark-dev
kubectl run cause-crash --image=busybox \
  --command -- sh -c 'echo "panic: missing config volume"; exit 1'
kubectl run bad-image   --image=nonexistent-registry.invalid/busybox:bad
kubectl run with-config --image=busybox --command -- sleep infinity
```

`cause-crash` (CrashLoopBackOff) is the canonical demo subject for the README GIF and most test transcripts. `bad-image` exercises the ImagePullBackOff path. `with-config` is the healthy-pod baseline.

## Test conventions

- Unit tests live next to the code they test (`foo.go` ↔ `foo_test.go`).
- Use `kubernetes/fake` for kube-layer tests. envtest support for full E2E coverage is planned.
- Pin behaviour with **why-it-matters comments** at the top of each test, not just what it asserts. Tests that document the *contract* age better than tests that document the *implementation*.
- Prefer substring assertions (`strings.Contains`) over full-output golden files — they survive prompt edits, theme tweaks, and other cosmetic changes.
- Security-sensitive paths (`internal/redact/`, `internal/diagnose/tools.go`, `internal/describe/service.go`) should have a test for both the no-leak contract and the no-false-positive baseline. See `internal/redact/redact_test.go` for the pattern.

## Style

- Conventional Commits in subject lines, ≤ 60 chars. Long form in the PR description.
- Go: idiomatic, `gofumpt`-formatted, `golangci-lint` clean against the project config.
- Every Go file begins with `// SPDX-License-Identifier: Apache-2.0`.
- Comments explain *why*, not *what*.

## Filing a bug

Use the bug template. Include kbark version (`kbark version`), Go version, OS/terminal, Kubernetes version, and a minimal reproduction.

## Security issues

Do not open a public issue. See `SECURITY.md`.
