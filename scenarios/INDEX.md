# Scenario registry

Auto-generated catalog. Do not edit by hand.


## Backend classics

| Scenario | Mechanism | Platform | Description |
|---|---|---|---|
| `disk-full` | ebpf | linux | Writes fail with ENOSPC after N bytes, exposing disk-space error handling bugs. |
| `fd-exhaustion` | ebpf | linux | open() returns EMFILE after N open descriptors, exposing file-descriptor-leak bugs. |
| `flaky-network` | ebpf | linux | Inject ECONNRESET on TCP recv, simulating flaky network conditions. |
| `slow-dns` | ebpf | linux | DNS resolution is delayed 500-5000ms with intermittent EAI_AGAIN, exposing DNS-timeout bugs. |

## LLM and gateway

| Scenario | Mechanism | Platform | Description |
|---|---|---|---|
| `context-window-squeeze` | proxy | any | Silently truncate large prompts at the SDK boundary, simulating silent context-window overflow. |
| `llm-api-degraded` | proxy | any | Inject 429/503/timeout into requests to OpenAI and Anthropic. |
| `malformed-json-response` | proxy | any | LLM returns 200 OK with syntactically invalid JSON in the body. |

## RAG and vector DB

| Scenario | Mechanism | Platform | Description |
|---|---|---|---|
| `embeddings-degraded` | proxy | any | Embeddings endpoint returns 5xx intermittently, breaking the RAG indexing pipeline. |
| `rag-corruption` | proxy | any | Vector DB returns stale or shuffled results, simulating retrieval-augmented generation going wrong. |

## Tool calls and subprocesses

| Scenario | Mechanism | Platform | Description |
|---|---|---|---|
| `tool-call-flaky` | ebpf | linux | Subprocess gets SIGPIPE / short read / OOM-killed, exposing tool-call resilience bugs. |
| `tool-permission-denied` | ebpf | linux | File operations fail with EACCES (permission denied). |
| `tool-slow` | ebpf | linux | Subprocess hangs for N seconds, exposing timeout-handling bugs in tool orchestration. |
