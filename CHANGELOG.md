# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-05-10

First public release. Five fault scenarios end-to-end across two
injection mechanisms (HTTPS proxy + eBPF), with worked examples in
Python and Node.js for each scenario.

### Added

- **CLI** — `faultkit run`, `scenario list/show`, `version`, `check`,
  `doctor`. Exit codes are part of the contract: `0` ok, `1` target
  failed under fault, `2` internal error, `3` fault never fired,
  `4` usage error.
- **HTTPS proxy injector** with per-run ECDSA P-256 CA written to a
  temp PEM and propagated via `HTTPS_PROXY` + `SSL_CERT_FILE`-family
  env vars. Three scenarios:
  - `llm-api-degraded` — 429 / 503 / timeout for OpenAI + Anthropic.
  - `malformed-json-response` — completion content body replaced with
    syntactically invalid JSON.
  - `llm-streaming-cutoff` — SSE stream truncated mid-token without
    `[DONE]`.
- **eBPF injector** (Linux 5.8+, kprobe-based, `BPF_MAP_TYPE_RINGBUF`
  events, process-tree propagation via `wake_up_new_task`). Two
  scenarios:
  - `flaky-network` — `ECONNRESET` on TCP recv.
  - `tool-permission-denied` — `EACCES` on `openat`.
- **Scenario YAML loader** (file via `--config`, builtins via
  `--scenario`); validation rejects mixed HTTP/syscall faults.
- **Reports** — terminal summary + JSON (`--report`).
- **Examples** — five `examples/<scenario>/` dirs each with a Python
  (`python/`) and a Node.js (`nodejs/`) sibling demonstrating the
  same intentional bug in both stacks.
- **Integration test** against real openai-python (`make
  test-integration`); skips when prereqs aren't installed.
- **Docker runner** (`make test-docker`) for reproducible CI.
- **GitHub Actions CI** running build / test / integration / `gosec`
  / `nilaway` on every PR and push to `main`.

### Project conventions established

- License: Apache 2.0. `pkg/extension` is the seam for the
  separately-licensed Pro repo; OSS code is Pro-unaware.
- `make sec` (`gosec` + `nilaway`) clean on every commit.
- Locked dependency set: `cilium/ebpf`, `spf13/cobra`, `spf13/viper`,
  `gopkg.in/yaml.v3`, `google/martian/v3`. No new deps without
  explicit approval.

[Unreleased]: https://github.com/faultkit/faultkit/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/faultkit/faultkit/releases/tag/v0.1.0
