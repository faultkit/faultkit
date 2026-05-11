# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/faultkit/faultkit/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/faultkit/faultkit/releases/tag/v0.1.0
