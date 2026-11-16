# Security Policy

## Reporting a vulnerability

Email **security@kbark.dev** with details. Do not open a public issue.

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce.
- The kbark version (`kbark version`) and the platform you observed it on.

You will get an acknowledgement within 72 hours. Coordinated disclosure window is **90 days** from acknowledgement, or sooner if a fix ships earlier.

## Scope

In scope: the `kbark` binary, the official Homebrew tap, the official Scoop bucket, GitHub release artifacts and their checksums.

Out of scope: third-party forks, packages, or distributions; user-supplied configuration that exposes secrets to the AI provider configured by the user; behavior of upstream AI providers.

## The autofix write path

`kbark serve` can change cluster state. What bounds that:

- **Six typed actions, nothing else.** Raise a memory limit, roll a
  workload back one revision, relax probe timing, delete an already-dead
  pod, replace an unhealthy pod whose siblings are healthy, uncordon a
  node. They live in `internal/remediate`. There is no shell, no exec, and
  no path from model output to an action: the AI provider is given no
  tools in this mode and its text is only displayed.
- **An approval per change.** The only caller of `remediate.Apply` is
  `Engine.Approve`. Proposals are validated by the API server as a dry run
  and bound to the target's UID and generation.
- **Policy narrows, never widens.** A policy file can make namespaces
  detect-only or restrict which actions are proposed. It cannot add
  actions or skip approval.
- **Least privilege.** `deploy/rbac.yaml` is the full permission set. It has
  no access to Secrets or ConfigMaps. `--confirm-oom` additionally needs
  `get` on `nodes/proxy`, a broad permission that is not in that file.
- **Redaction.** Evidence and log excerpts pass through `internal/redact`
  before they are sent to the AI provider, and are marked as untrusted data in
  the prompt.
- **Audit.** Every detection, proposal, approval and outcome is appended to a
  JSON-lines file (mode 0600).

## The console

- **Sign-in.** With `--auth-file`, every API call needs a session and
  writes need the approver or admin role. Passwords are stored as bcrypt
  hashes. Sessions are random 256-bit tokens held in server memory, sent
  as an `HttpOnly`, `SameSite=Strict` cookie (also `Secure` when
  `--public-url` is https), valid for 12 hours. The name recorded in the
  audit log is the account's, not something the browser supplies.
- **Lockout.** Five failed sign-ins from one client for one username lock
  that pair out for a minute. Behind a reverse proxy pass `--trust-proxy`
  so the client address comes from the proxy's headers; do not pass it
  when clients can reach kbark directly, because they could forge the header.
- **Open mode.** Without `--auth-file` the operator name is self-declared.
  kbark then refuses to listen on anything but loopback unless
  `--insecure-no-auth` is given.
- **Cross-site requests.** The API rejects a foreign `Origin`, non-JSON
  writes and, on loopback, a foreign `Host`.

Known limits:

- Accounts live in a file; there is no single sign-on, no password reset
  flow and no second factor.
- Sessions are lost when kbark restarts.
- **Local mode uses your kubeconfig credentials**, which are usually broader
  than `deploy/rbac.yaml`. The server deployment in `deploy/server/` uses
  a service-account token bound to that role instead.

Reports about the write path and the console are in scope.

## No bug bounty

kbark is a single-maintainer open-source project. There is no monetary bounty. Contributors who report a confirmed vulnerability will be credited in the release notes for the fix, unless they ask to remain anonymous.

## Supported versions

Only the latest minor release line receives security fixes.
