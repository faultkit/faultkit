# faultkit examples

Five small projects, one per v0.1 scenario. Each ships an agent with a
deliberate bug that shows up only when the corresponding fault is
injected. Every example has a Python (`python/`) and a Node.js
(`nodejs/`) sibling so you can pick whichever stack matches your
workflow.

The pattern for every example:

```bash
# 1. Naked test — happy path doesn't trigger the bug. Passes.
(cd python && pip install -r requirements.txt && pytest .)
(cd nodejs && npm install && npm test)

# 2. Under faultkit — fault injection exercises the unhappy path
#    the agent code didn't handle. Fails.
faultkit run --config scenario.yaml -- python3 -m pytest python/    # proxy
faultkit run --config scenario.yaml -- node --test nodejs/test.js   # proxy
faultkit run --scenario <name>     -- python3 -m pytest python/     # eBPF
faultkit run --scenario <name>     -- node --test nodejs/test.js    # eBPF
```

| Example | Scenario | Mode | What the bug is |
|---|---|---|---|
| [llm-api-degraded](./llm-api-degraded/) | `llm-api-degraded` | proxy | Agent doesn't retry on 429 |
| [malformed-json-response](./malformed-json-response/) | `malformed-json-response` | proxy | Agent trusts `JSON.parse` without validation |
| [llm-streaming-cutoff](./llm-streaming-cutoff/) | `llm-streaming-cutoff` | proxy | Streaming consumer ignores `finish_reason` |
| [flaky-network](./flaky-network/) | `flaky-network` | eBPF (Linux 5.8+) | TCP client doesn't retry on `ECONNRESET` |
| [tool-permission-denied](./tool-permission-denied/) | `tool-permission-denied` | eBPF (Linux 5.8+) | File-reading tool swallows `EACCES` silently |

## Running

Each example is self-contained. Run pytest from inside `python/` (and
`npm test` from inside `nodejs/`) to avoid import collisions across
examples.

Python 3.10+ for the Python siblings. Node 20+ for the Node siblings —
the proxy examples set a global undici `ProxyAgent` from `HTTPS_PROXY`
so faultkit's per-run proxy intercepts localhost mock traffic; without
that, `globalThis.fetch` would bypass the proxy for loopback hosts.
