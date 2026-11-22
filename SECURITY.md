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

- **Ten typed actions, nothing else.** Raise a memory limit, raise an
  ephemeral-storage limit, roll a workload back one revision, relax probe
  timing, change a pull policy from `Never` to `IfNotPresent`, correct a
  Service selector, delete an already-dead pod, replace an unhealthy pod
  whose siblings are healthy, force-delete a pod stuck on a dead node,
  uncordon a node. They live in `internal/remediate`. There is no shell, no exec, and
  no path from model output to an action: the AI provider is given no
  tools in this mode and its text is only displayed.
- **A named person behind every change.** `remediate.Apply` is reached
  only through an approval. By default that is one click per fix. Where
  policy sets `standing_approvals: low-risk`, an approver may instead
  approve one low-risk action type for one workload in advance; fixes
  applied that way are recorded under that person's name, the grant
  expires, any approver can revoke it, and it is revoked automatically
  the first time such a fix fails. Medium-risk fixes always need a click.
- **Pull requests write to Git, not the cluster.** With a GitHub token
  kbark can open a pull request against a manifest file. The token is
  read from an environment variable, never logged, and should be scoped
  to the repositories kbark may propose to (contents and pull requests).
  Which repository and file is taken from the workload's annotations, so
  anyone who can annotate a workload can direct a pull request at any
  repository the token can write to: scope the token accordingly.
- **Checked before every write.** Proposals are validated by the API server as a dry run
  and bound to the target's UID and generation.
- **Policy narrows.** A policy file can make namespaces detect-only or
  restrict which actions are proposed. It cannot add actions. The one
  thing it can relax is to allow standing approvals of low-risk fixes,
  described above; it cannot remove the person.
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
  hashes. Sessions are random 256-bit tokens, sent
  as an `HttpOnly`, `SameSite=Strict` cookie (also `Secure` when
  `--public-url` is https), valid for 12 hours. The name recorded in the
  audit log is the account's, not something the browser supplies.
  Sessions can survive a restart (`--session-file`, default next to the
  state file); only a SHA-256 hash of each token is written to disk.
- **Single sign-on.** The users file's optional `oidc` block adds
  OpenID Connect sign-in with PKCE (go-oidc verifies the ID token's
  signature, issuer, audience, expiry and nonce, and `email_verified`
  must be true when the provider sends the claim). Handshake state is
  kept server-side, is single-use, and expires after 5 minutes; a
  successful sign-in becomes an ordinary kbark session, so roles and the
  audit trail work unchanged. Failures redirect to the login page with
  one of kbark's own short codes — provider error text is never
  reflected into the page.
- **Metrics.** `--metrics-addr` serves Prometheus text on its own
  loopback listener, separate from the console: scrapers do not sign
  in, and the console gains no unauthenticated route. A non-loopback
  address is refused without `--metrics-insecure-public`.
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

- Password accounts live in a file; there is no password reset flow and
  no second factor. Single sign-on, when configured, has no second
  factor of its own either — the provider's is the only one.
- Metrics on a loopback listener are unauthenticated by design; whatever
  can read them learns cluster names and incident counts.
- **Local mode uses your kubeconfig credentials**, which are usually broader
  than `deploy/rbac.yaml`. The server deployment in `deploy/server/` uses
  a service-account token bound to that role instead.

Reports about the write path and the console are in scope.

## No bug bounty

kbark is a single-maintainer open-source project. There is no monetary bounty. Contributors who report a confirmed vulnerability will be credited in the release notes for the fix, unless they ask to remain anonymous.

## Supported versions

Only the latest minor release line receives security fixes.
