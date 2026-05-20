# Contributing to kbark

Thanks for considering a contribution. kbark is a single-maintainer project right now, so the bar for merge is high and the loop is fast.

## Before you open a PR

- **Open an issue first** for anything larger than a typo or a one-file fix. Unscoped PRs will be closed without review.
- Check `ROADMAP.md` — if your idea isn't on it and isn't a bug fix, the answer may be "not yet."
- v1 is scoped tight on purpose. New feature ideas land in `ROADMAP.md` only after discussion.

## Build, test, lint

```sh
task build        # builds ./bin/kbark
task test         # go test ./...
task lint         # golangci-lint run
task run          # go run ./cmd/kbark
```

Pre-commit hooks live in `lefthook.yml`. Install once with `lefthook install` after cloning.

## Style

- Conventional Commits in subject lines, ≤ 60 chars. Long form in the PR description.
- Go: idiomatic, `gofumpt`-formatted, `golangci-lint` clean against the project config.
- Every Go file begins with `// SPDX-License-Identifier: Apache-2.0`.
- Comments explain *why*, not *what*.

## Filing a bug

Use the bug template. Include kbark version (`kbark version`), Go version, OS/terminal, Kubernetes version, and a minimal reproduction.

## Security issues

Do not open a public issue. See `SECURITY.md`.
