# Releasing kbark

A release is a tagged commit on `main`. Everything downstream of the tag
(cross-platform binaries, SHA256SUMS, signature, GitHub Release page,
brew formula update, scoop manifest update) is automated by the
`.github/workflows/release.yml` workflow calling GoReleaser.

## One-time setup

These prerequisites need to exist before the first `v*` tag is pushed.

1. **Tap + bucket repos** — create empty repos at:
   - `github.com/shivangtanwar/homebrew-tap`
   - `github.com/shivangtanwar/scoop-bucket`

   GoReleaser writes the formula / manifest files into them on first
   publish; they don't need any starter content.

2. **PATs in this repo's secrets** — the workflow needs cross-repo
   write access (the default `GITHUB_TOKEN` can't push to the tap
   repos):

   - `HOMEBREW_TAP_GITHUB_TOKEN` — fine-grained PAT scoped to
     `shivangtanwar/homebrew-tap` with **contents: write**
   - `SCOOP_BUCKET_GITHUB_TOKEN` — same shape, scoped to
     `shivangtanwar/scoop-bucket`

   Add at <https://github.com/shivangtanwar/kbark/settings/secrets/actions>.

3. **Cosign** is installed inside the workflow via
   `sigstore/cosign-installer` — no key management needed; signing is
   keyless via the GitHub OIDC token.

## Cutting a release

```bash
# Sanity-check the config without publishing
goreleaser check
goreleaser release --snapshot --clean      # produces ./dist locally

# When ready, tag from main
git checkout main && git pull --ff-only
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The push triggers `release.yml`. Once green:

- A GitHub Release at `https://github.com/shivangtanwar/kbark/releases/tag/v0.1.0`
- Six binaries attached:
  - `kbark_0.1.0_linux_amd64.tar.gz`
  - `kbark_0.1.0_linux_arm64.tar.gz`
  - `kbark_0.1.0_darwin_amd64.tar.gz`
  - `kbark_0.1.0_darwin_arm64.tar.gz`
  - `kbark_0.1.0_windows_amd64.zip`
- `SHA256SUMS`, `SHA256SUMS.sig`, `SHA256SUMS.pem`
- Updated brew formula at `shivangtanwar/homebrew-tap/Formula/kbark.rb`
- Updated scoop manifest at `shivangtanwar/scoop-bucket/bucket/kbark.json`

After the release:

```bash
# macOS / Linux
brew install shivangtanwar/tap/kbark
kbark --version

# Windows
scoop bucket add kbark https://github.com/shivangtanwar/scoop-bucket
scoop install kbark
kbark.exe --version
```

## Verifying the signature

```bash
RELEASE=v0.1.0
gh release download "$RELEASE" -p 'SHA256SUMS*'

cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/shivangtanwar/kbark/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --signature SHA256SUMS.sig \
  --certificate SHA256SUMS.pem \
  SHA256SUMS
```

Cosign prints `Verified OK` if the signature was produced by
**this** repo's workflow on **this** tag — protecting against
typosquatted forks or compromised CI in unrelated repos.

## What goes in `--snapshot`

`goreleaser release --snapshot --clean` builds the same matrix
locally without:

- Pushing anything to GitHub
- Updating the tap / bucket
- Signing (no OIDC token available outside CI)

Useful for sanity-checking the `.goreleaser.yaml` after edits —
artifacts land in `./dist` and can be inspected.

## Rolling back

Two paths:

1. **Yank the release** (preferred): edit the GitHub Release to
   mark it as a draft / pre-release. Brew/scoop users who haven't
   updated yet won't pick it up. Then push a fixed tag (`v0.1.1`).

2. **Delete the tag + release** (only for catastrophic bugs):
   ```bash
   gh release delete v0.1.0 --cleanup-tag --yes
   ```
   Be aware that anyone who already installed v0.1.0 keeps the
   binary; only the published artifacts are gone.

## Binary size

The stripped release binary sits around ~95–100MB. Most of that is
client-go + k8s.io/api + kubectl/describe — the cost of speaking
fluent Kubernetes. The strategy doc's "<30MB" target was
aspirational and predates the kubectl/describe dep; we can revisit
in a polish PR if it ever becomes a real complaint (UPX
compression, etcd-style on-demand kind loading, or trimming
kubectl/describe in favour of a hand-rolled per-kind formatter).
