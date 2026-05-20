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

## No bug bounty

kbark is a single-maintainer open-source project. There is no monetary bounty. Contributors who report a confirmed vulnerability will be credited in the release notes for the fix, unless they ask to remain anonymous.

## Supported versions

Only the latest minor release line receives security fixes.
