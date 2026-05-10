# Using faultkit in CI

faultkit is a single static binary with deterministic exit codes —
exactly what CI scripts want to branch on.

---

## Install in CI

Pin a version. The release tarballs contain a single `faultkit`
binary:

```bash
VERSION=v0.1.0
curl -sSL "https://github.com/faultkit-dev/faultkit/releases/download/$VERSION/faultkit_${VERSION#v}_linux_amd64.tar.gz" \
  | sudo tar -xz -C /usr/local/bin faultkit
faultkit version
```

For Linux runners, that's enough for proxy mode. For eBPF scenarios,
the runner also needs Linux 5.8+ and either root or `CAP_BPF` (GitHub
Actions hosted runners satisfy both).

## GitHub Actions

```yaml
name: Fault tests

on:
  pull_request:
  push:
    branches: [main]

jobs:
  faultkit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'

      - name: Install your test deps
        run: pip install -r requirements.txt

      - name: Install faultkit
        run: |
          VERSION=v0.1.0
          curl -sSL "https://github.com/faultkit-dev/faultkit/releases/download/$VERSION/faultkit_${VERSION#v}_linux_amd64.tar.gz" \
            | sudo tar -xz -C /usr/local/bin faultkit

      - name: Sanity check
        run: faultkit check

      - name: Run agent tests under fault
        run: |
          faultkit run \
            --scenario llm-api-degraded \
            --report report.json \
            -- pytest tests/agent/

      - name: Upload fault report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: faultkit-report
          path: report.json
```

## Branching on exit code

```bash
faultkit run --scenario flaky-network -- pytest .
case $? in
  0) echo "passed under fault" ;;
  1) echo "test failed under fault — fix or retry needed" ;;
  3) echo "no fault fired — check your match clause" ;;
  *) echo "faultkit error" ;;
esac
```

Exit codes are part of the contract — see
[quickstart.md](./quickstart.md) for the full list.

## Failing the build only when faults uncover bugs

Sometimes you want fault-injection runs to be advisory rather than
blocking. The split:

- Exit `0`: target survived the fault. Don't block the build.
- Exit `1`: target failed under fault. **This is the signal you care
  about** — block the build, attach the report.
- Exit `3`: no fault fired. Likely a misconfigured match. Block the
  build (your test isn't covering what you thought).

```yaml
- name: Run agent tests under fault
  run: |
    set +e
    faultkit run --scenario llm-api-degraded --report report.json -- pytest tests/agent/
    rc=$?
    case $rc in
      0|3) exit 0 ;;   # passed, or no fault fired (advisory)
      1)   exit 1 ;;   # target failed under fault — block the build
      *)   exit $rc ;; # any other code (faultkit internal, usage) — surface it
    esac
```

Or just always-fail and treat the report artifact as the diagnostic.

## Multiple scenarios in one job

Use a matrix:

```yaml
strategy:
  matrix:
    scenario: [llm-api-degraded, malformed-json-response, flaky-network]
steps:
  - uses: actions/checkout@v4
  # ... install steps ...
  - name: Run faultkit
    run: |
      faultkit run --scenario ${{ matrix.scenario }} --report report-${{ matrix.scenario }}.json -- pytest tests/
```

## Using a custom scenario

Commit your scenario YAML alongside the tests:

```yaml
- name: Run with custom scenario
  run: faultkit run --config .faultkit/scenarios/openai-503.yaml -- pytest tests/
```

YAML schema: [docs/yaml-schema.md](./yaml-schema.md).

## Running multiple scenarios

For now, run faultkit multiple times — once per scenario. v0.2 may
add scenario composition; v0.1 keeps it simple.

## Cost expectations

faultkit runs single-shot, foreground, against your existing test
process. There's no daemon, no shared state, no agent. Per-run cost is
~50 ms startup + the proxy MITM overhead (~1 ms per request) for
proxy scenarios, or kernel-side syscall hooks (sub-microsecond) for
eBPF scenarios. CI impact is dominated by your test runtime, not by
faultkit.
