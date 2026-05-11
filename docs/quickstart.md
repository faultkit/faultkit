# Quickstart

Five minutes from zero to a deterministic fault injection against your
own code.

---

## Install

Grab the binary for your platform from the [releases page](https://github.com/faultkit/faultkit/releases),
or build from source:

```bash
git clone https://github.com/faultkit/faultkit.git
cd faultkit
make build
sudo install -m 0755 bin/faultkit /usr/local/bin/faultkit
```

Verify:

```bash
faultkit version
faultkit check
```

`faultkit check` reports which injection modes are available on this
host. On macOS or any non-Linux box you'll see proxy mode only; on
Linux 5.8+ both modes light up (eBPF needs `CAP_BPF` or root).

## Pick a scenario

```bash
faultkit scenario list
```

Output:

```
flaky-network            [ebpf]   Inject ECONNRESET on TCP recv, simulating flaky network conditions.
llm-api-degraded         [proxy]  Inject 429/503/timeout into requests to OpenAI and Anthropic.
llm-streaming-cutoff     [proxy]  Streaming chat completion drops the connection mid-token without [DONE].
malformed-json-response  [proxy]  LLM returns 200 OK with syntactically invalid JSON in the body.
tool-permission-denied   [ebpf]   File operations fail with EACCES (permission denied).
```

The `[proxy]` and `[ebpf]` tags tell you which mechanism the scenario
needs. Proxy works everywhere; eBPF needs Linux 5.8+.

To see a scenario's full YAML:

```bash
faultkit scenario show llm-api-degraded
```

## Run it against your code

Wrap your test command (or any process) under `faultkit run`:

```bash
faultkit run --scenario llm-api-degraded -- pytest tests/agent/
```

faultkit starts an HTTPS proxy with a per-run CA, sets `HTTPS_PROXY`
and `SSL_CERT_FILE` in the child environment, runs your command, then
prints a summary:

```
=== faultkit summary ===
scenario:     llm-api-degraded
target:       pytest tests/agent/
duration:     14.2s
faults fired: 7
target exit:  0 (PASS)
```

`target exit: 0` means your code handled the faults. `target exit: 1`
means a fault propagated as an unhandled error — which is usually what
you want to discover. Exit codes:

| Code | Meaning |
|---|---|
| 0 | OK — target passed under fault |
| 1 | Target failed under fault |
| 2 | faultkit internal error |
| 3 | No fault fired (target didn't hit the matched traffic) |
| 4 | Usage error |

## Capture a JSON report for CI

```bash
faultkit run \
  --scenario llm-api-degraded \
  --report report.json \
  -- pytest tests/agent/
```

`report.json` lists every fault decision (host, path, fired/skipped,
timestamp). Upload it as a CI artifact for post-mortem.

See [docs/ci.md](./ci.md) for the full GitHub Actions recipe.

## Try a custom scenario

Save this as `scenario.yaml`:

```yaml
name: my-scenario
description: 50% of OpenAI calls return 503 Service Unavailable
experiments:
  - name: api-down
    fault:
      http_status: 503
    match:
      host: api.openai.com
      path: /v1/chat/completions
    probability: 0.5
```

Run with `--config` instead of `--scenario`:

```bash
faultkit run --config scenario.yaml -- pytest tests/agent/
```

Schema reference: [docs/yaml-schema.md](./yaml-schema.md).

## Worked examples

The repo's [`examples/`](../examples/) directory has five end-to-end
projects, one per builtin scenario, with both Python and Node.js
siblings. Each ships an agent with a deliberate bug that surfaces only
under fault injection.

```bash
cd examples/llm-api-degraded
(cd python && pip install -r requirements.txt && pytest .)               # passes
faultkit run --config scenario.yaml -- python3 -m pytest python/         # fails
```

## Next

- Browse [docs/scenarios.md](./scenarios.md) for what each scenario
  actually injects and when to reach for it.
- Read the [YAML schema reference](./yaml-schema.md) to author your
  own scenarios.
- Wire faultkit into your CI: [docs/ci.md](./ci.md).
