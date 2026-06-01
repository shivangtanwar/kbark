<!--
Title should follow Conventional Commits, <= 60 chars.
Example: feat(tui): bubbletea shell + footer
-->

## What changed

One paragraph. What this PR does, in plain English.

## Why

One paragraph. The motivation, the bug, or the milestone this lands on.

## How tested

- [ ] `task test` passes
- [ ] `task lint` passes
- [ ] Manually verified against minikube / kind / EKS (delete what doesn't apply)
- [ ] New tests added where appropriate
- [ ] If this touches `internal/redact/`, `internal/diagnose/tools.go`, or `internal/describe/service.go`: no-leak tests still pass and any new code paths have a test for the no-leak contract

## Linked issue

Fixes #
