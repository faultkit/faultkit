# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.2] - 2026-06-18

The second maintenance release. The headline is **base-URL injection** —
a second way to route a target's LLM traffic through faultkit, for SDK
clients that ignore `HTTPS_PROXY`. Upgrading is drop-in: `--base-url` is
opt-in, and the CLI surface, exit codes, and scenario YAML schema are
unchanged, so anything that ran against v0.1.1 runs unchanged here.

### Added

- **Base-URL injection mode (`--base-url`).** Instead of relying on the
  target honoring `HTTPS_PROXY`, faultkit points the client's SDK at
  itself via provider base-URL env vars (`OPENAI_BASE_URL` /
  `OPENAI_API_BASE`, `ANTHROPIC_BASE_URL`) and injects faults at the
  origin. This reaches clients that ignore proxy env — Node
  `fetch`/`undici`, and SDKs spawned as subprocesses. Ships with a
  provider registry (OpenAI, Anthropic) and an origin-mode request
  handler, and is documented in the README. `--base-url` applies to
  HTTP/proxy scenarios only; combining it with a syscall (eBPF) scenario
  or `--mode=ebpf` is a clear usage error.
- **Anthropic Messages API parity for the built-in proxy scenarios.**
  `malformed-json-response` and `llm-streaming-cutoff` now also fault
  Anthropic `/v1/messages` traffic (streaming drops without
  `message_stop`; a malformed body shaped like an Anthropic message), not
  just OpenAI `/v1/chat/completions`.
- **Base-URL Node example**
  (`examples/llm-api-degraded/nodejs/baseurl-client.js`) and an
  end-to-end base-URL integration test.

### Changed

- **A run now warns when no request ever reached faultkit.** Previously a
  target that silently ignored `HTTPS_PROXY` (or never used the injected
  base URL) could exit green with no fault fired — a misleading pass.
  faultkit now prints a mode-specific warning to stderr in that case (and
  suggests `--base-url` for proxy-ignoring clients). Exit codes are
  unchanged.

### Internal

- Committed the Node examples' `package-lock.json` files so `npm ci`
  works in CI and the examples build reproducibly.
- Added the base-URL injection design and an agentic gap-analysis note
  under `docs/internal/`.

### Build / supply chain

- **Switched to a local-release model.** The CI release workflow
  (`.github/workflows/release.yml`) is removed; releases are now cut from
  a maintainer's machine via goreleaser, so no publish credential is
  stored as a GitHub secret — the GitHub release uses the maintainer's
  `gh` auth, AUR uses the AUR signing key, and the Homebrew tap is pushed
  over ambient GitHub SSH. The goreleaser config gains AUR
  (`faultkit-bin`) and Homebrew-formula publishers; the release-download
  docs were corrected to match the goreleaser asset names.

## [0.1.1] - 2026-06-03

The first maintenance release after v0.1.0. Upgrading is drop-in: no
changes to the CLI surface, exit codes, or scenario YAML schema, so
anything that ran against v0.1.0 runs unchanged against v0.1.1.

What moves from v0.1.0 to v0.1.1: `run` and `check` now agree on mode
availability and fail fast with a clear reason instead of an opaque
loader error; `--verbose` actually logs (it was declared but ignored in
v0.1.0); the docs were corrected to match the implementation; test
coverage broadened to the eBPF scenarios and the streaming path; and the
build, CI, and supply chain were hardened.

### Fixed

- `faultkit run` now fails fast with a clear reason when eBPF mode is
  unavailable on the host (missing capabilities, non-x86-64, or a kernel
  without `CONFIG_BPF_KPROBE_OVERRIDE`) instead of an opaque loader error
  during startup — `run` and `check` now agree.
- `faultkit check` reports eBPF as unavailable on non-x86-64 hosts (the
  BPF programs hook `__x64_sys_*` kprobes).
- `--verbose` now works: it logs injector lifecycle (mode, scenario,
  kprobe attach, target PID registration) and each fault as it fires. It
  was previously declared but ignored.

### Changed

- README corrected to match the implementation: eBPF uses kprobes (not
  tracepoints) on `recvmsg`/`recvfrom`/`openat`; documented the
  `CONFIG_BPF_KPROBE_OVERRIDE` kernel requirement; dropped the
  unnecessary `cap_sys_ptrace` from the `setcap` example (the three BPF
  caps are sufficient, verified on-host); noted the proxy serves
  HTTP/1.1.

### Added

- Project logo (`assets/faultkit-logo.svg` / `.png`) and a README hero.

### Internal

- Removed unused leaf-minting code from the proxy CA (martian mints the
  leaf certs). Added end-to-end integration tests for both eBPF
  scenarios (flaky-network, tool-permission-denied) and the
  streaming-cutoff goroutine-leak contract.

### Security / supply chain

- All GitHub Actions pinned to commit SHAs and bumped to Node 24
  runtimes; goreleaser pinned to `~> v2`.
- golangci-lint added to the CI gate — lint and security scans run on
  every push and pull request.
- Dependencies vendored (`vendor/`); builds run hermetically from the
  in-repo vendor tree.
- Added `SECURITY.md` (private vulnerability reporting + supply-chain
  posture) and a "Supply chain security" policy section in `CLAUDE.md`.
- Pinned the Go toolchain and bumped it to 1.25.11, clearing two
  standard-library advisories reachable from faultkit: GO-2026-5037
  (`crypto/x509` candidate-hostname parsing) and GO-2026-5039
  (`net/textproto`). Release binaries are built with the patched
  toolchain.

## [0.1.0] - 2026-05-11

First public release.

faultkit is a fault injection toolkit for AI agents and backend
services. Wrap any command under `faultkit run` and the binary
injects realistic failures into the target's HTTP traffic or system
calls — letting you exercise error-handling paths your agents have
never actually run against production-shaped failures.

### Highlights

- **One binary, single-shot.** `faultkit run --scenario X -- <your tests>`.
  No daemon, no sidecar, no infrastructure. Deterministic exit codes
  for CI branching.
- **Two injection mechanisms.** An HTTPS MITM proxy with a per-run CA
  for application-layer faults (LLM 429s, malformed JSON, dropped
  streams), and eBPF kprobes for syscall-layer faults (ECONNRESET,
  EACCES). Pick whichever matches the failure you're targeting.
- **Five scenarios out of the box.** All shipping in the binary; see
  `faultkit scenario list`.
- **Worked examples.** Every scenario has a Python (`python/`) and
  Node.js (`nodejs/`) reference implementation in `examples/`,
  each demonstrating an intentional bug that surfaces only under
  fault injection.
- **Reports.** Terminal summary by default, or `--report report.json`
  for CI artifacts.

### Scenarios

| Scenario | Mode | What it injects |
|---|---|---|
| `llm-api-degraded` | proxy | 429 / 503 from OpenAI + Anthropic with realistic `Retry-After` headers |
| `malformed-json-response` | proxy | LLM response body replaced with syntactically invalid JSON |
| `llm-streaming-cutoff` | proxy | SSE chat completion drops mid-token without `[DONE]` |
| `flaky-network` | eBPF (Linux 5.8+) | `ECONNRESET` on TCP `recvmsg` / `recvfrom` |
| `tool-permission-denied` | eBPF (Linux 5.8+) | `EACCES` on `openat` |

You can also author your own scenarios in YAML and pass them via
`--config scenario.yaml`. Schema reference: [docs/yaml-schema.md](./docs/yaml-schema.md).

### Platform support

- **Proxy mode:** macOS and Linux. No special privileges.
- **eBPF mode:** Linux 5.8+ with `CAP_BPF` (or root). x86_64.

### Install

Binaries on the [releases page](https://github.com/faultkit/faultkit/releases/tag/v0.1.0)
for `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`.
Or:

```bash
go install github.com/faultkit/faultkit/cmd/faultkit@v0.1.0
```

### Exit codes (contract)

| Code | Meaning |
|---|---|
| 0 | Target passed under fault |
| 1 | Target failed under fault |
| 2 | faultkit internal error |
| 3 | No fault fired — target didn't hit matched traffic |
| 4 | Usage error |

### Compatibility promise

The CLI surface, exit codes, and scenario YAML schema are part of the
user contract starting v0.1.0. Additive changes are minor releases;
breaking changes require a major version bump.

### Documentation

- [Quickstart](./docs/quickstart.md) — install and run a first scenario in 5 minutes.
- [Scenarios](./docs/scenarios.md) — per-scenario reference.
- [YAML schema](./docs/yaml-schema.md) — author your own scenarios.
- [Using faultkit in CI](./docs/ci.md) — GitHub Actions recipes.

[Unreleased]: https://github.com/faultkit/faultkit/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/faultkit/faultkit/releases/tag/v0.1.2
[0.1.1]: https://github.com/faultkit/faultkit/releases/tag/v0.1.1
[0.1.0]: https://github.com/faultkit/faultkit/releases/tag/v0.1.0
