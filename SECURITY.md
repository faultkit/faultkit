# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub
issues, discussions, or pull requests.**

Report privately via GitHub's **Private Vulnerability Reporting**: the
repository's **Security** tab → **Report a vulnerability**. (Maintainers:
enable this under Settings → Code security → Private vulnerability
reporting.)

We aim to acknowledge a report within 3 business days and to share a
remediation timeline after triage. Please allow a reasonable window for a
fix and release before public disclosure — we follow coordinated
disclosure. Include the affected version or commit, a description, repro
steps or a proof of concept, and the impact.

## Supported versions

faultkit is pre-1.0; the API, CLI surface, and scenario schema may still
change between minor versions. Security fixes land on the latest released
minor only.

| Version | Supported |
|---|---|
| latest `0.1.x` | ✅ |
| older tags | ❌ — upgrade to the latest |

## Build and supply-chain security

faultkit treats build-pipeline integrity as part of its security
surface. The project holds its build and CI/CD to the standards below;
the authoritative, enforced rules live in
[`CLAUDE.md`](./CLAUDE.md) → "Supply chain security".

- **SHA-pinned GitHub Actions.** Every CI/CD action is pinned to a full
  commit SHA (with the version in a trailing comment), never a mutable
  tag or branch.
- **Pinned tooling.** Linters and security scanners (gosec, nilaway,
  govulncheck, golangci-lint) are pinned to exact, immutable versions —
  never `@latest`.
- **Mandatory checks.** Linting and security scans run on every push and
  pull request; a failure blocks merge.
- **Least-privilege CI.** Workflows declare minimal `permissions:`, and
  secrets are scoped to the jobs that need them.
- **Dependency cooldown.** New dependency, action, and tool versions are
  adopted after a short cooldown (~10 days), not on their release day.
- **Vendored dependencies.** Dependencies are vendored and builds run
  from `vendor/`, so they are reviewable in diffs and not fetched from
  the network at build time.

## Scope and threat model

faultkit is a developer and CI **fault-injection** tool, not a production
security control. By design it MITMs the target process's TLS using a
**per-run, ephemeral CA scoped to that process only** (see the README
"How it works") and never installs a CA into any system trust store. Run
it only against services and traffic you own.
