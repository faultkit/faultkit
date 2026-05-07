# faultkit examples

Five small Python projects, one per v0.1 scenario. Each one ships an
agent with a deliberate bug that shows up only when the corresponding
fault is injected.

The pattern for every example:

```bash
# 1. Naked test — happy path doesn't trigger the bug. Passes.
pytest .

# 2. Under faultkit — fault injection exercises the unhappy path
#    the agent code didn't handle. Fails.
faultkit run --scenario <name> -- pytest .          # eBPF examples
faultkit run --config scenario.yaml -- pytest .     # proxy examples
```

| Example | Scenario | Mode | What the bug is |
|---|---|---|---|
| [llm-api-degraded](./llm-api-degraded/) | `llm-api-degraded` | proxy | Agent doesn't retry on 429 |
| [malformed-json-response](./malformed-json-response/) | `malformed-json-response` | proxy | Agent trusts `json.loads` without validation |
| [llm-streaming-cutoff](./llm-streaming-cutoff/) | `llm-streaming-cutoff` | proxy | Streaming consumer ignores `finish_reason` |
| [flaky-network](./flaky-network/) | `flaky-network` | eBPF (Linux 5.8+) | TCP client doesn't retry on `ECONNRESET` |
| [tool-permission-denied](./tool-permission-denied/) | `tool-permission-denied` | eBPF (Linux 5.8+) | File-reading tool swallows `EACCES` silently |

## Running

Each example is self-contained. From inside the example directory:

```bash
pip install -r requirements.txt
pytest .
```

Python 3.10 or newer. Run pytest from inside the example dir to avoid
import name collisions across examples.

## Status

faultkit's injectors land in v0.1 phases 2–7. The agents and tests are
ready to run today; the `faultkit run …` invocations above produce the
expected failures once the proxy and eBPF injectors ship.
