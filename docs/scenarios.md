# Scenarios

faultkit ships scenarios that map to real production failure modes in
AI agent and backend systems. Each scenario is a YAML recipe that
faultkit injects into your application while it runs, so you
can verify your code survives failures you've written handlers for
but never actually seen.

This document covers every scenario that ships today. For planned
scenarios, see the bottom of this page.

---

## At a glance

The LLM scenarios use HTTPS proxy injection; the syscall-level scenarios
use eBPF injection (Linux).

**Cross-provider LLM** — each fires against every provider that has a fixture for
its failure mode: OpenAI, Anthropic, and Bedrock, except `llm-streaming-cutoff`,
whose `streaming-cutoff` mode has no Bedrock fixture (OpenAI and Anthropic only).
Narrow with `--provider` (see [Failure modes and providers](#failure-modes-and-providers)):

| Scenario | Mode | What it tests |
|---|---|---|
| `llm-api-degraded` | proxy | LLM provider returns HTTP 429 (rate limited) |
| `malformed-json-response` | proxy | LLM returns syntactically invalid JSON |
| `malformed-tool-use` | proxy | Tool call with malformed / schema-violating arguments |
| `max-tokens-truncation` | proxy | Truncated 200 the agent treats as complete |
| `llm-streaming-cutoff` | proxy | Streaming response drops mid-token |
| `llm-empty-response` | proxy | 200 with no content (`null` / empty content array) |
| `context-window-overflow` | proxy | Provider rejects the prompt as over the context window (HTTP 400) |
| `llm-slow-first-token` | proxy | Response starts 8–15s late (streamed or not) |

**Anthropic-specific** — failures Claude surfaces with no OpenAI equivalent:

| Scenario | Mode | What it tests |
|---|---|---|
| `anthropic-overloaded` | proxy | HTTP 529 `overloaded_error` |
| `anthropic-stream-error` | proxy | SSE `error` event mid-stream, no `message_stop` |
| `anthropic-tool-use-cutoff` | proxy | `tool_use` truncated by `max_tokens` |
| `anthropic-refusal` | proxy | 200 with `stop_reason: "refusal"` |
| `anthropic-request-too-large` | proxy | HTTP 413 `request_too_large` |

**Bedrock-specific** — Bedrock failures with no cross-provider equivalent:

| Scenario | Mode | What it tests |
|---|---|---|
| `bedrock-model-timeout` | proxy | HTTP 408 `ModelTimeoutException` |
| `bedrock-service-unavailable` | proxy | HTTP 503 `ServiceUnavailableException` |

**RAG and vector DB** — embeddings endpoints on OpenAI and Bedrock (Anthropic
has no embeddings API), and vector queries on Pinecone and Qdrant Cloud
(forward-proxy only):

| Scenario | Mode | What it tests |
|---|---|---|
| `embeddings-degraded` | proxy | Embeddings endpoint returns 503 |
| `rag-stale-results` | proxy | Vector query returns matches for deleted / nonexistent document ids |

**Syscall-level** — Linux, eBPF:

| Scenario | Mode | What it tests |
|---|---|---|
| `flaky-network` | eBPF | TCP connections drop with `ECONNRESET` |
| `tool-permission-denied` | eBPF | File operations fail with `EACCES` |

All of them are free, open-source, and built into the faultkit binary.
Run `faultkit scenario list` to see what's available on your installation.

**Recipes** — copy-paste YAML for hosts only you know, run with `--config`
(see [Recipes](#recipes)):

| Scenario | Mode | What it tests |
|---|---|---|
| `memory-write-failure` | proxy (forward-proxy only) | A store write is acknowledged but never stored; the next read is stale |
| `mcp-tool-schema-mismatch` | proxy (forward-proxy only) | An MCP tool result's `structuredContent` no longer matches the tool's `outputSchema` |
| `gateway-timeout` | proxy (forward-proxy only) | Your LLM gateway answers 30 s late, or hangs 60 s and then answers 504 |
| `subagent-timeout` | proxy (forward-proxy only) | An HTTP call to a sub-agent never returns within the run |

---

## How scenarios work

Every scenario has the same shape:

```yaml
name: my-scenario              # required, kebab-case identifier
description: |                  # optional, shown in `scenario list`
  One-line description.

requires:                      # optional, platform constraints
  platform: linux              # advisory; declared but not validated or enforced
  kernel_min: "5.8"            # advisory; declared but not validated or enforced

experiments:                   # one or more fault recipes
  - name: "human-readable name"
    fault:                     # what failure to inject
      http_status: 429
      response_headers:
        Retry-After: "30"
    match:                     # which traffic to apply it to
      host: "api.openai.com"
      path: "/v1/*"
    probability: 0.2           # 0.0-1.0; how often to fire
```

Multiple experiments in the same scenario are evaluated in order — the
first match wins. If the matching experiment's dice roll declines, the
request passes through unfaulted; later experiments aren't tried.
Probability is independent per request: a `0.2` probability fires
roughly 20% of matching requests, with the dice rolled fresh each time.

For the full schema reference, see [docs/yaml-schema.md](./yaml-schema.md).

---

## Failure modes and providers

The builtin LLM scenarios aren't written per-provider. Each is a **failure
mode** — a provider-agnostic recipe — and the concrete response shape (the exact
body, status, and headers a given SDK parses) comes from a per-provider
**fixture**. A scenario references a mode by name; faultkit supplies the
fixtures.

```yaml
experiments:
  - name: rate-limited
    failure: rate-limited     # the failure mode
    probability: 0.2          # no `provider:` → fires for every provider with a fixture
```

- **No provider** → the failure runs against every provider that has a fixture
  for that mode.
- **A provider** — the `provider:` field, or `--provider` on the CLI — narrows it
  to one:

  ```bash
  faultkit run --scenario malformed-json-response --provider anthropic -- pytest tests/
  ```

The rule is **one scenario, N fixtures**: supporting a new provider means adding
a fixture, never a new scenario. Failures with no cross-provider equivalent
(e.g. Anthropic's 529 `overloaded_error`) ship as their own scenarios — they're
just modes with a single-provider fixture. Raw `fault` + `match` scenarios
(custom hosts and paths) are unaffected and behave exactly as before.

LLM providers with a fixture: `openai`, `anthropic`, and `bedrock` (Amazon Bedrock,
`bedrock-runtime.<region>.amazonaws.com`). **Bedrock is forward-proxy only** —
it authenticates with SigV4, which the forward proxy leaves intact but base-URL
mode would break, so `--base-url` does not target Bedrock. `--provider bedrock`
selects Bedrock's fixtures in forward-proxy mode; `llm-api-degraded` fires a
`ThrottlingException` (429) against it.

The 200-body scenarios (`malformed-json-response`, `max-tokens-truncation`,
`malformed-tool-use`, `llm-empty-response`, `anthropic-tool-use-cutoff`,
`anthropic-refusal`) answer with one complete, non-streaming JSON response, so
they target **non-streaming calls**. A client that sent `stream: true` gets that
JSON instead of an SSE stream, and how it reacts depends on the SDK. For
streaming paths use `llm-streaming-cutoff` or `anthropic-stream-error`.
The vector databases `pinecone` (index hosts, `*.pinecone.io`) and `qdrant`
(Qdrant Cloud, `*.cloud.qdrant.io`) are forward-proxy only as well. Their only
fixtures are for `stale-results`, so the LLM modes never fan out to them — see
[Vector DB scenarios](#vector-db-scenarios).

---

## Proxy scenarios

These scenarios use faultkit's HTTPS proxy. Your application connects
to the LLM provider as normal, but faultkit intercepts the connection,
terminates TLS with a per-run certificate, and rewrites the response.
From your application's perspective, the failure is indistinguishable
from a real provider failure — because at the HTTP layer, that's what
it is.

### `llm-api-degraded`

**The marquee scenario.** Your LLM provider rate-limits you under
load. Your retry logic kicks in. Did it kick in correctly? Did the
retry preserve reasoning state? Did it back off? Did it give up
gracefully?

faultkit returns HTTP 429 to roughly 20% of requests: with
`Retry-After: 30` for OpenAI and Anthropic, and as a
`ThrottlingException` for Bedrock.

Real production failure mode: OpenAI and Anthropic rate limits during
traffic spikes, and Bedrock throttling when requests exceed your
account's quotas.

**What it tests:**

- Retry logic with backoff and jitter
- Per-tenant rate limiting and circuit breakers
- Fallback provider activation (if you have one)
- User-facing degradation messaging
- Cost controls (does your code retry forever?)

**YAML:**

```yaml
name: llm-api-degraded
description: Rate-limit requests to OpenAI and Anthropic (HTTP 429, Retry-After 30) and Bedrock (HTTP 429 ThrottlingException).
experiments:
  - name: rate-limited
    failure: rate-limited
    probability: 0.2
```

**Run it:**

```bash
faultkit run --scenario llm-api-degraded -- pytest tests/agent/
```

**What good behavior looks like:**

Your tests pass. Your agent retries with appropriate backoff, surfaces
a graceful error after exhausting retries, and never silently produces
output based on a failed call.

**What bad behavior looks like:**

A test fails because your agent retried with corrupted state and
produced a confidently wrong answer. That's the bug `llm-api-degraded`
is built to find.

---

### `malformed-json-response`

The model returns a 200 OK with a body that looks like JSON but isn't
quite. Trailing commas. Unescaped quotes. Truncated structures. Mixed
content where there should be only structured output.

This is the failure mode that breaks tool-calling agents most often
in production. The model is supposed to return:

```json
{"action": "lookup_user", "args": {"id": 42}}
```

But returns:

```json
{"action": "lookup_user", "args": {"id": 42}
```

Your JSON parser throws. Your agent's exception handler may or may not
have a recovery path. If it doesn't, the entire reasoning loop is
lost and the user gets an error — or worse, your code "recovers"
into an undefined state.

**What it tests:**

- JSON parser error handling
- Schema validation before action dispatch
- Retry-with-stricter-instruction logic
- Graceful degradation when the model genuinely can't produce valid output
- The boundary between "the model failed" and "the agent failed"

**YAML:**

```yaml
name: malformed-json-response
description: LLM returns invalid JSON when the agent expects structured output.
experiments:
  - name: "trailing comma"
    fault:
      http_status: 200
      response_body: |
        {"choices":[{"message":{"content":"{\"action\":\"lookup\",\"args\":{\"id\":42},}"}}]}
    match:
      host: "api.openai.com"
      path: "/v1/chat/completions"
    probability: 0.15
```

**Run it:**

```bash
faultkit run --scenario malformed-json-response -- pytest tests/agent/
```

**What good behavior looks like:**

Your code catches the parse error, surfaces a structured error to the
agent's loop, and either retries with a stricter prompt or gives up
gracefully. The user never sees a stack trace.

**What bad behavior looks like:**

The exception propagates up to the request handler. The user sees a
500. Or, worse, your code "fixes" the JSON with a regex hack and the
agent acts on a guess at what the model meant.

---

### `llm-streaming-cutoff`

You're using streaming chat completions. The connection establishes,
tokens start flowing, and then — partway through — the stream drops.
What does your application do?

In production this happens when: provider-side worker dies mid-stream,
network path degrades and the keep-alive fails, a load balancer
recycles the connection, your own infrastructure's idle timeout fires.
The stream's `data: [DONE]` sentinel never arrives.

This scenario configures the proxy to forward the stream normally for
N content deltas (≈ tokens); lifecycle events like `message_start`
don't count toward the budget. The connection then closes without
sending the terminator.

The fault fires only when faultkit actually cuts a stream, that is, when
an SSE response reaches N content deltas (80 in the built-in scenario). A
non-streaming response, or a stream that ends with fewer than N deltas,
reaches the target unchanged and doesn't count as fired. If no stream in
the run gets that far, the run proves nothing: the verdict is
`invalid_evidence`, and faultkit exits `3` unless the target itself failed.
Make sure the test asks for a reply longer than N deltas. In forward-proxy
mode the proxy buffers its writes to the client, so a client that
disconnects mid-stream can still be counted as fired.

**What it tests:**

- Streaming completion detection (do you wait for `[DONE]` or just for "no more bytes"?)
- Partial-output handling (does your UI show "..." or commit the partial as final?)
- Retry on incomplete streams
- Duplicate-prevention on retry (if you retry, does the user see two responses?)
- Frontend state recovery

**YAML:**

```yaml
name: llm-streaming-cutoff
description: Streaming response drops the connection mid-token.
experiments:
  - name: "drop after 80 tokens"
    fault:
      stream_cutoff_tokens: 80
    match:
      host: "api.openai.com"
      path: "/v1/chat/completions"
    probability: 0.1
```

**Run it:**

```bash
faultkit run --scenario llm-streaming-cutoff -- pytest tests/streaming/
```

**What good behavior looks like:**

Your streaming consumer detects the missing terminator and treats the
output as incomplete — either retrying or surfacing partial output
clearly marked as truncated. No partial response is treated as final.

**What bad behavior looks like:**

The frontend shows the partial response as if it's complete and
commits it to the conversation history. The next turn's context now
contains a truncated assistant message, and the agent compounds the
error.

---

### `llm-slow-first-token`

The provider accepts the request and then takes 8–15 seconds to start
answering. Does your application wait, time out, retry, or tell the user
what's happening?

In production this happens when: the provider is queueing requests under
load, a large prompt takes long to prefill, a cold model replica spins up,
or a gateway in between is slow.

faultkit holds each matched request for 8 s plus a random 0–7 s, then
forwards it unchanged to the real provider. The reply itself is the
provider's own; only its start is late. In base-URL mode a streamed
response then streams as the provider sends it. In forward-proxy mode
the proxy buffers the client side in 4 KiB writes, so a short stream can
arrive in one piece at the end. A non-streamed reply arrives whole. The fault
applies to OpenAI `/v1/chat/completions`, Anthropic `/v1/messages`, and
Bedrock `/model/*` (forward-proxy mode only, like every Bedrock fixture).
Each delayed request counts as one fired fault, when its delay starts,
even if the client gave up or the run ended before the delay ran out.

The run ending (the target exiting) cuts every pending delay short and
drops those connections without forwarding them. faultkit doesn't notice
a client that gives up during the delay of a `POST`, though: if the run
is still going when the delay runs out, the request is still sent to the
provider, so it can be a billed call. See
[the delay fields](./yaml-schema.md#delay-level-neutral) for the full
semantics.

**What it tests:**

- Client timeouts (is there one, and is it longer than a slow-but-healthy
  reply?)
- Retry on timeout (does a retry duplicate a request the provider is
  still answering?)
- User feedback while waiting (spinner, "still working" message)
- Overall deadlines in agent loops that make several LLM calls

**YAML:**

```yaml
name: llm-slow-first-token
description: LLM requests are held 8–15s before they reach the provider.
experiments:
  - name: slow-first-token
    failure: slow-first-token
    probability: 0.2
```

The equivalent raw experiment for one provider:

```yaml
experiments:
  - name: "openai slow first token"
    fault:
      delay_ms: 8000
      delay_jitter_ms: 7000
    match:
      host: "api.openai.com"
      path: "/v1/chat/completions"
    probability: 0.2
```

**Run it:**

```bash
faultkit run --scenario llm-slow-first-token -- pytest tests/
```

**What good behavior looks like:**

Your application waits within a deliberate timeout, shows progress, and
if it gives up, it does so cleanly: one retry at most, no duplicated side
effects, and an honest message to the user.

**What bad behavior looks like:**

A default timeout nobody chose fires first, the agent retries in a tight
loop and multiplies the load, or a request handler upstream of the agent
times out while the LLM call carries on and its result is thrown away.

---

### `malformed-tool-use`

The model returns a tool call the agent can't act on: for OpenAI the
`function.arguments` string is itself invalid JSON; for Anthropic the `tool_use`
block's `input` violates the tool's schema. It's a 200 — the SDK doesn't
complain — so the failure only bites when the agent parses or dispatches the call.

**What it tests:** JSON-parse and schema-validation guards around tool dispatch;
telling "the model emitted a bad call" apart from "the tool failed".

---

### `max-tokens-truncation`

A 200 whose content is valid but truncated, flagged only by
`finish_reason: "length"` (OpenAI) / `stop_reason: "max_tokens"` (Anthropic). An
agent that consumes the content without checking the finish/stop reason treats a
half-answer as complete.

**What it tests:** checking the finish/stop reason before using output;
continuation or retry on truncation.

---

### `llm-empty-response`

A 200 whose envelope parses cleanly but carries no content. OpenAI returns
`message.content: null` with `finish_reason: "content_filter"`; Anthropic
returns an empty `content` array with an ordinary `stop_reason: "end_turn"`;
Bedrock Converse returns an empty `output.message.content` with
`stopReason: "end_turn"`. The response is HTTP 200, but SDK behavior varies:
plain calls hand back the empty or `null` content, while parsing helpers can
raise on the finish reason (OpenAI Python's `chat.completions.parse()` raises
`ContentFilterFinishReasonError` on `finish_reason: "content_filter"`).
Non-streaming calls only (see [Failure modes and providers](#failure-modes-and-providers)).

**What it tests:** both paths. For an empty result: checking for empty or
`null` content before using it, not indexing the first content block, not
storing the blank as an answer, and not retrying without limit a request that
keeps coming back empty. For an SDK that raises: handling that exception
instead of crashing the agent loop. In both cases, branching on the
finish/stop reason.

---

### `context-window-overflow`

The provider refuses the request as too long for the model's context window.
A matching request gets HTTP **400** with the provider's own error body:
OpenAI an `invalid_request_error` with `code: "context_length_exceeded"`;
Anthropic an `invalid_request_error` whose message starts `prompt is too long`;
Bedrock Converse a `ValidationException` (`x-amzn-errortype` header) with
`Input is too long for requested model.` The request is rejected outright.
Nothing is truncated, and no partial answer comes back.

This is a **probabilistic, fixed** fault: each matching request independently
gets the same 400 with probability 0.2, whatever the real prompt length. A
shortened retry can be rejected again, and an unchanged retry can get through
when the dice skip it. faultkit does not inspect the prompt or compare
retries, so it does not check that your agent reduced its context.

Matched paths: OpenAI `/v1/chat/completions`, Anthropic `/v1/messages`,
Bedrock `/model/*/converse`. The real APIs reject an oversized prompt before
any stream starts, so a `stream: true` request to OpenAI or Anthropic gets the
same 400. Bedrock `ConverseStream` and `InvokeModel` calls are not matched.

**What it tests:** that the agent recognizes a context-length 400 rather than
treating it as a generic or transient error, and that the error reaches the
user or a recovery path instead of an empty or invented answer. If your agent
recovers by trimming history, summarizing, or chunking retrieved context, your
target's own tests must assert that; the fixture fires regardless of prompt
size and does not validate the recovery.

---

### `embeddings-degraded`

The embeddings endpoint returns HTTP **503**, intermittently (probability 0.2).
OpenAI's `/v1/embeddings` gets its `server_error` body; Bedrock `InvokeModel`
calls on an embedding model (a model ID containing `embed`, such as
`amazon.titan-embed-text-v2:0` or `cohere.embed-english-v3`) get a 503 with
`x-amzn-errortype: ServiceUnavailableException`. Chat and Converse calls on the
same hosts pass through. Anthropic has no embeddings API, so there is no
Anthropic fixture, and Bedrock is forward-proxy only.

**What it tests:** retry and backoff on the indexing and query-embedding paths;
what retrieval does when it can't embed the query — fail loudly, fall back to
keyword search, or (the bug) answer with no retrieved context.

---

## Anthropic-specific scenarios

Failures Claude surfaces that have no direct OpenAI equivalent, so each ships as
its own scenario (Anthropic-only). All are fixture-driven — see
[Failure modes and providers](#failure-modes-and-providers).

### `anthropic-overloaded`

HTTP **529 `overloaded_error`** — Anthropic's "overloaded, try again" under load.
OpenAI never returns 529, so retry/back-off logic that only special-cases
429/503 sails right past it.

**What it tests:** that your retry classifier treats 529 as retryable.

### `anthropic-stream-error`

A well-formed SSE stream that emits an **`event: error`** (overloaded_error)
partway through and then stops — *no* `message_stop`. The bytes are clean, so a
consumer that equates "stream ended" with "success" misses the error.

**What it tests:** that you parse SSE `error` events, not just `message_stop`.

### `anthropic-tool-use-cutoff`

A `tool_use` block whose turn was cut off by the token limit: `stop_reason` is
**`max_tokens`**, not `tool_use`. The tool call is incomplete and must not be
dispatched.

**What it tests:** checking `stop_reason` before acting on a `tool_use` block.

### `anthropic-refusal`

A 200 with **`stop_reason: "refusal"`** — the model declined. The body is a valid
200 with little or no usable content, so code that treats 200 as "answer" stores
blanks or retries a request that will keep being refused.

**What it tests:** branching on `stop_reason`; not retrying a refusal.

### `anthropic-request-too-large`

HTTP **413 `request_too_large`** for an oversized request.

**What it tests:** oversized-prompt handling — chunking or graceful failure
rather than an unhandled 413.

---

## Bedrock-specific scenarios

Failures Amazon Bedrock surfaces that have no direct OpenAI or Anthropic
equivalent, so each ships as its own scenario (Bedrock-only). All are
fixture-driven — see [Failure modes and providers](#failure-modes-and-providers).

### `bedrock-model-timeout`

HTTP **408 `ModelTimeoutException`** — the model did not respond within the
allotted time. Bedrock surfaces this as an HTTP 408 with the exception type in
the `x-amzn-errortype` response header.

**What it tests:** that your retry/timeout logic handles 408 from Bedrock
distinctly from network-level timeouts; that `x-amzn-errortype` is parsed when
it matters.

### `bedrock-service-unavailable`

HTTP **503 `ServiceUnavailableException`** — the service is temporarily unable
to process the request. Returned under load or during internal Bedrock issues.

**What it tests:** that your code retries 503 from Bedrock with appropriate
backoff rather than treating it as a permanent failure.

---

## Vector DB scenarios

RAG failures at the retrieval layer, against two forward-proxy-only
providers. Fixture-driven — see
[Failure modes and providers](#failure-modes-and-providers).

### `rag-stale-results`

A vector query succeeds — HTTP 200, a well-formed body — but its matches
point at documents that are meant not to exist: deleted from the source
corpus while still in the index, or never there at all. In production this
happens when deletes don't reach the index, or a re-index lags the corpus.

faultkit answers the query endpoint with a canned body in the vendor's
documented response shape:

| Provider | Host | Endpoint | Stale ids |
|---|---|---|---|
| `pinecone` | `*.pinecone.io` (index hosts) | `/query` | `faultkit-stale-doc-1`, `faultkit-stale-doc-2` in `matches[]` |
| `qdrant` | `*.cloud.qdrant.io` (Qdrant Cloud, any port) | `/collections/*/points/query` | UUIDs `00000000-0000-4000-8000-00000000fa01` and `…fa02` in `result.points[]` |

Each match carries `metadata` (Pinecone) or a `payload` (Qdrant) with
`source` and `text` keys; the text says the document is stale. faultkit
doesn't read the request, so the number of matches, their scores, and the
Pinecone `namespace` (`""`, the default namespace) are fixed.

The ids are chosen to be unlikely in a real index, but faultkit doesn't look
at your store: make sure no record with these ids exists there, or the run
isn't testing a missing document.

**What it tests:** what your pipeline does with retrieved ids it can't
resolve — whether it looks them up, rejects them or falls back, and whether
it cites them. faultkit only injects the response; the proof comes from your
target's own assertions on that lookup, rejection/fallback and citation
behavior. A target that only checks that the query succeeded passes, and so
does one that uses the inline `metadata`/`payload` text without looking the
ids up at all. A fired fault doesn't show deletion propagation, authorization
isolation, retry safety, or a general RAG invariant.

**Limits:**

- **Forward-proxy only.** The vector-DB client must route through
  `HTTPS_PROXY` and trust faultkit's CA (`SSL_CERT_FILE`,
  `REQUESTS_CA_BUNDLE`, …); `--base-url` doesn't target vector databases.
  Qdrant's Python client (`qdrant-client`, REST) reads both from the
  environment. Pinecone's Python SDK reads neither: pass them to the client,
  `Pinecone(api_key=…, proxy_url=os.environ["HTTPS_PROXY"],
  ssl_ca_certs=os.environ["SSL_CERT_FILE"])`, or its queries go straight to
  Pinecone and faultkit warns that no request reached its proxy. (Checked
  with `qdrant-client` 1.19.1 and `pinecone` 10.0.0.) If no query reaches
  faultkit, nothing fires, and zero fired faults never produce a passing
  run: faultkit exits `3` if the target otherwise succeeded, or `1` if the
  target itself failed — with verdict `invalid_evidence` either way.
- **REST only.** gRPC clients (Pinecone's `GrpcIndex`, Qdrant on port 6334
  or with `prefer_grpc=True`) aren't faulted. If they honour
  `HTTPS_PROXY`/`grpc_proxy`, they fail to connect at all: the proxy only
  offers HTTP/1.1, and gRPC needs HTTP/2, so the TLS handshake ends with
  `no_application_protocol`, and faultkit warns that the handshake failed.
- **One endpoint per provider.** Pinecone's integrated-inference search
  (`/records/namespaces/*/search`), Qdrant's legacy `/points/search`, and
  the batch `/points/query/batch` return different shapes and aren't faulted.
- **Qdrant Cloud only.** A self-hosted cluster doesn't match
  `*.cloud.qdrant.io`. Write a raw experiment for your host instead (host
  without the port; some clients, Go's `net/http` among them, never proxy
  `localhost`):

  ```yaml
  name: stale-qdrant-selfhosted
  experiments:
    - name: stale-qdrant
      fault:
        http_status: 200
        response_body: '{"result":{"points":[{"id":"00000000-0000-4000-8000-00000000fa01","version":7,"score":0.91,"payload":{"text":"stale"}}]},"status":"ok","time":0.002}'
      match:
        host: qdrant.internal.example.com
        path: /collections/*/points/query
      probability: 0.2
  ```

  The same form lets you return the metadata keys your framework expects.

---

## eBPF scenarios

These scenarios use faultkit's eBPF injector. Small kernel programs
hook syscall kprobes and rewrite return values for processes
inside your target's PID tree. The target sees a real `ECONNRESET`,
a real `EACCES`, because that's what the kernel returned.

eBPF scenarios require Linux 5.8 or newer on x86-64, with `CAP_BPF`,
`CAP_NET_ADMIN`, and `CAP_PERFMON` capabilities (or root). Where that's
missing, `faultkit check` reports eBPF mode as unavailable and these
scenarios fail with exit code `2`.

### `flaky-network`

TCP connections drop intermittently with `ECONNRESET`. The classic
backend chaos scenario, applied at the syscall level so it's
indistinguishable from a real flaky network.

Real production failure mode: a noisy neighbor in your cloud
provider's network, a load balancer cycling backends, an upstream
service being deployed, a NAT timing out idle connections.

**What it tests:**

- Connection retry logic in HTTP clients
- Database driver reconnection
- Connection pool health checks
- Circuit breakers on TCP-level failures
- Distinguishing transient from permanent failures

**YAML:**

```yaml
name: flaky-network
description: Inject ECONNRESET on TCP recv operations.
requires:
  platform: linux
  kernel_min: "5.8"
experiments:
  - name: "tcp recv reset"
    fault:
      errno: ECONNRESET
    match:
      syscall: recvmsg
    probability: 0.1
```

**Run it:**

```bash
faultkit run --scenario flaky-network -- ./my-backend-test
```

**What good behavior looks like:**

Your service detects the reset, classifies it as transient, retries
with backoff, and succeeds on the retry. Connection pools recycle
the bad connection. No requests are dropped to the user.

**What bad behavior looks like:**

A reset propagates through three layers of code as a 500. Or worse,
your service treats it as a permanent failure and stops trying,
when retrying would have worked.

---

### `tool-permission-denied`

Your agent invokes a tool that needs to read a file, write a log,
or access a directory — and the syscall returns `EACCES`. Your tool
implementation throws. Your agent's loop has to handle it.

This is the failure mode that exposes agents running with insufficient
permissions, hitting filesystem ACLs in unexpected places, or
encountering files that exist in dev but not in the deployment
environment.

**What it tests:**

- Tool subprocess error propagation
- Agent loop's tolerance for tool failures
- Whether "tool failed" gets distinguished from "tool said no"
- Retry-with-different-args logic
- Graceful surfacing of permission errors to the user

**YAML:**

```yaml
name: tool-permission-denied
description: File operations fail with EACCES (permission denied).
requires:
  platform: linux
  kernel_min: "5.8"
experiments:
  - name: "openat eacces"
    fault:
      errno: EACCES
    match:
      syscall: openat
    probability: 0.05
```

**Run it:**

```bash
faultkit run --scenario tool-permission-denied -- ./run-agent-tests.sh
```

**What good behavior looks like:**

The tool surfaces the error to the agent. The agent recognizes a
permission error as distinct from a "no results" or "task complete"
outcome. The user sees a clear error or the agent retries with a
different approach.

**What bad behavior looks like:**

The tool's exception bubbles up as an unhandled error. The agent's
loop terminates without explanation. The user sees a generic
"something went wrong."

---

## Recipes

Some failures only make sense against a host faultkit can't know in
advance, such as your own memory store, MCP server, gateway or sub-agent.
These ship as recipes: copy the YAML into a file, point it at your host,
and run it with `--config`. They aren't builtins, so
`faultkit run --scenario` and `faultkit scenario list` don't know them.

### `memory-write-failure`

Your agent saves state between steps (a memory, a checkpoint, a
conversation summary) to an HTTP store. The store answers the write
with a success, but the data never lands. The next step reads the old
value and carries on as if the write had happened.

In production this happens when a write is acknowledged before it is
durable: a replica that loses it, a read served before replication
catches up, an async write API whose background job is dropped.

The recipe uses `match.method`. Writes (`POST`, `PUT`, `PATCH`) to your
store's path get a synthetic success and never reach the store. Reads
(`GET`) pass through to the real store, which still holds the old data.

**Limits:**

- **Needs a faultkit release newer than 0.1.3.** `match.method` is new;
  0.1.3 and earlier ignore the field without a warning, so they fault
  every request to the host and path, reads included.
- **HTTP-backed stores only.** The agent must reach the store through a
  REST/HTTP API. File-based memory, database wire protocols and gRPC
  clients aren't covered.
- **Forward-proxy mode only.** `--base-url` only routes OpenAI and
  Anthropic traffic, so it can't reach a store host. The store client
  must honor `HTTPS_PROXY` and trust the CA bundle faultkit injects
  (`SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, …). Some
  clients never send loopback traffic through a proxy (Go's `net/http`
  is one), so a store on `localhost` may never see the fault.
- **Writes are matched by method and path, not by body.** A store API
  that sends reads and writes as `POST` to the same path, such as a
  Redis-over-HTTP command endpoint, can't be split this way.
- **Lost writes only.** A dropped write never reaches the store, so
  duplicate effects from retries aren't tested. Nor are lost
  acknowledgements: a write that lands but whose response never arrives
  isn't simulated.

**What it tests:**

- Read-your-writes checks after a store write
- Whether the agent trusts a write acknowledgement it never verifies
- Recovery when the state read back isn't what was written

**YAML** (save as `memory-write-failure.yaml`):

```yaml
name: memory-write-failure
description: Memory-store writes are acknowledged but never stored, so later reads are stale.
experiments:
  - name: "create acknowledged, not stored"
    fault:
      http_status: 200
      # Replace with the body your store returns for a successful write.
      response_body: '{"id":"mem_123","status":"ok"}'
    match:
      host: "memory.example.com"    # replace with your store's API host
      path: "/v1/memories*"         # replace with your store's write path
      method: POST
    probability: 1.0
  - name: "replace acknowledged, not stored"
    fault:
      http_status: 200
      response_body: '{"id":"mem_123","status":"ok"}'
    match:
      host: "memory.example.com"
      path: "/v1/memories*"
      method: PUT
    probability: 1.0
  - name: "update acknowledged, not stored"
    fault:
      http_status: 200
      response_body: '{"id":"mem_123","status":"ok"}'
    match:
      host: "memory.example.com"
      path: "/v1/memories*"
      method: PATCH
    probability: 1.0
```

Replace the host and path with your store's; `host` is required, a match
without it never fires. Set `response_body` to what your store returns
for a successful write, copied from its API reference or from a real
response, and use its status code too (often `201` for a create). If you
leave `response_body` out, faultkit answers a 2xx write with `{}`. A
client that reads fields from the write response (an id, a version, the
stored record) may then fail loudly with a parse or validation error.
That tests a different failure from a write that is silently lost.

A match names one method, so the recipe has one experiment per write
method. Delete the ones your store doesn't use. `probability: 1.0` drops
every write, so the run is repeatable; lower it to drop only some.

**Run it:**

```bash
faultkit run --config memory-write-failure.yaml -- pytest tests/agent/
```

Each dropped write is a fired event in the report. If none of the
agent's writes reach faultkit (wrong host or path, or a client that
bypasses the proxy), the run proves nothing: the verdict is
`invalid_evidence`, and faultkit exits `3` unless the target itself
failed.

A fired fault with exit `0` only proves something if your test checks
the store. When the agent reports success, the test should read the
state back from the store with a fresh client (not the agent's
in-process cache) and fail if it's stale. A test that only checks that
nothing crashed passes with stale state, and the report then says
`invariant_proven_under_fault`. At `probability: 1.0` the agent's
retries are dropped too, so the only correct outcome is an error the
agent surfaces: the test should expect that error, not fail on it.

**What good behavior looks like:**

The agent notices. It reads the record back, or checks the version the
store returns, and reports an error when the write didn't land. If it
retries first, the retry is dropped too at `probability: 1.0`. It never
acts on state it believes it saved but didn't.

**What bad behavior looks like:**

The next step reads the old memory and acts on it: the user's updated
preference is ignored, a finished task is done again, a summary loses
the last turn. Nothing in the logs says a write failed.

### `mcp-tool-schema-mismatch`

Your agent calls tools on an MCP server. In `tools/list` the server
declares each tool's `outputSchema`, and a `tools/call` result carries
`structuredContent` that should match it. Then the server is upgraded,
or another version is deployed behind the same URL, and a field changes
type: the temperature arrives as `"22.5"` instead of `22.5`.

A client that validates `structuredContent` against the declared
`outputSchema` rejects the result with an error. A client that doesn't
validate passes the wrong types on to the agent.

The recipe sets `response_mutation: mcp-schema-mismatch`. faultkit
forwards every request to your real MCP server. In the response to a
`tools/call` request, it changes the type of each top-level value in
`result.structuredContent`:

| Value | Becomes |
|---|---|
| number | its text, as a string: `22.5` → `"22.5"` |
| string | the number it holds (`"65"` → `65`), otherwise `true` |
| boolean | `"true"` or `"false"` |
| object, array, `null` | its JSON text, as a string: `{"id":7}` → `"{\"id\":7}"` |

Everything else stays as the server sent it: the message's `id`,
`content`, `isError` and `_meta`, and any other message in the same
response, such as a progress notification. Requests other than
`tools/call` (`initialize`, `tools/list`, notifications) pass through
untouched.

**Limits:**

- **Needs a faultkit release newer than 0.1.3.** `response_mutation` is
  new; 0.1.3 and earlier don't know the field and reject the experiment
  as an empty fault (exit `4`).
- **Streamable HTTP only.** A server the client starts as a subprocess
  and talks to over stdio never goes through faultkit. Neither does the
  older HTTP+SSE transport, which sends the result on a separate event
  stream instead of in the response to the `tools/call` request.
- **Forward-proxy mode only.** `--base-url` only routes OpenAI and
  Anthropic traffic, so it can't reach an MCP server. The MCP client
  must send its traffic through `HTTPS_PROXY` (or `HTTP_PROXY` for an
  `http://` server) and, over HTTPS, trust the CA bundle faultkit
  injects (`SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`,
  …). Some HTTP clients ignore those variables, and some never send
  loopback traffic through a proxy (Go's `net/http` is one), so a server
  on `localhost` may never see the fault.
- **`structuredContent` only.** The text in `content[]`, which carries
  the same data for clients that don't read `structuredContent`, isn't
  changed. A client that only reads the text content sees no mismatch,
  yet the run still counts the fault as fired, so it can report
  `invariant_proven_under_fault` without having tested anything. A
  result without `structuredContent` is left alone, and fires nothing.
- **A late result counts while the client is still running.** A tool
  result changed after the target exited doesn't count. But if the
  client gives up on a `tools/call` (a timeout, a cancelled task) and
  keeps running, the late result is still changed and counts as fired,
  although the agent never read it.
- **Bounded and uncompressed.** A request body or JSON response over
  1 MiB, an event-stream line over 1 MiB, a compressed response and a
  batched JSON-RPC request all pass through unchanged. In an event
  stream each `data:` line is checked on its own, so a message split
  across several `data:` lines isn't changed.
- **The changed message is re-encoded.** It comes back compact, with
  its object members in sorted order. JSON doesn't depend on member
  order, but a client that compares raw bytes notices.

**What it tests:**

- Whether the agent checks tool results against the declared schema
- What the agent does when its MCP client rejects a result: report a
  tool error and carry on, or crash
- Code that assumes the types of structured results, such as
  `result["temperature"] > 30`

**YAML** (save as `mcp-tool-schema-mismatch.yaml`):

```yaml
name: mcp-tool-schema-mismatch
description: MCP tool results come back with structuredContent that breaks the tool's outputSchema.
experiments:
  - name: "tool result breaks its outputSchema"
    fault:
      response_mutation: mcp-schema-mismatch
    match:
      host: "mcp.example.com"    # replace with your MCP server's host
      path: "/mcp"               # replace with your MCP endpoint path
      method: POST
    probability: 1.0
```

Replace the host and path with your MCP server's; `host` is required, a
match without it never fires. `method: POST` keeps the experiment off
the `GET` event stream a client may open for server messages; every
`tools/call` is a `POST`. `probability: 1.0` changes every tool result,
so the run is repeatable; lower it to change only some.

**Run it:**

```bash
faultkit run --config mcp-tool-schema-mismatch.yaml -- pytest tests/agent/
```

Each changed tool result is a fired event in the report. Without
`delay_ms`, nothing else is: a `tools/call` whose result has no
`structuredContent` passes through and adds no event. Without
`delay_ms`, if no tool result was changed (wrong host or path, a client
that bypasses the proxy, a server that compresses its responses, or
tools without `structuredContent`), the run proves nothing: the verdict
is `invalid_evidence`, and faultkit exits `3` unless the target itself
failed.

At `probability: 1.0` every tool result is wrong, including retries, so
the only correct outcome is the agent reporting a tool error. A test
that expects that error and passes gets `invariant_proven_under_fault`
(exit `0`). If the agent crashes on the rejected result, the target
fails and the verdict is `silent_failure_confirmed` (exit `1`).

If you add `delay_ms` too, every matched request is held, `initialize`
and `tools/list` included, and counts as fired when its delay starts.
The changed tool result then adds no second event. So the run counts as
fired even if no tool result was changed, and the verdict can't show
that the mismatch reached the client.

**What good behavior looks like:**

The agent treats a result that doesn't match its schema as a failed
tool call. It reports the tool error, retries or falls back, and never
acts on the mistyped values.

**What bad behavior looks like:**

The client's validation error escapes and ends the agent's loop. Or,
with a client that doesn't validate, the agent compares the string
`"22.5"` to a number, adds to it, or stores it where a number belongs,
and nothing in the logs says the tool's output was wrong.

### `gateway-timeout`

Your application reaches its LLM through a gateway (LiteLLM, Portkey,
your own proxy) rather than calling the provider directly. The gateway
answers slowly, or holds the request until something gives up and then
answers `504 Gateway Timeout`. Does your application wait within a
deliberate timeout, retry once, fall back, or hang?

In production this happens when the provider behind the gateway is
slow, the gateway itself is overloaded and queueing, or a reverse proxy
in front of it gives up first: nginx, for one, waits 60 s for a response
by default (`proxy_read_timeout`) and then answers 504.

The recipe comes in two variants. Use one at a time: faultkit applies
the first experiment that matches a request, so a second experiment on
the same host and path would never fire.

- **Slow:** each matched request is held for 30 s, then forwarded
  unchanged to your gateway, so the reply starts 30 s late.
- **Hang, then 504:** each matched request is held for 60 s, then
  answered with a synthetic `504`. Your gateway never sees the request.

**Limits:**

- **Needs a faultkit release newer than 0.1.3.** `delay_ms` is new; 0.1.3
  and earlier ignore the field without a warning, so the 504 variant
  answers at once and the slow variant forwards with no delay.
- **Forward-proxy mode only.** `--base-url` only routes OpenAI and
  Anthropic traffic, so it can't reach a gateway host: with this recipe
  the run fails at start (exit `2`). Your app's gateway client must honor
  `HTTPS_PROXY` (`HTTP_PROXY` for an `http://` gateway) and trust the CA
  bundle faultkit injects (`SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`,
  `NODE_EXTRA_CA_CERTS`, …).
- **A gateway on `localhost` is reached only by some clients.** faultkit
  doesn't set `NO_PROXY` in forward-proxy mode, so the target inherits
  yours, and a client that reads it skips the hosts it lists. Unless
  `NO_PROXY` lists the gateway's host, curl and Python's `urllib` send
  loopback requests through the proxy, and `host: "localhost"` (or
  `"127.0.0.1"`, whichever your app's URL uses) works. Go's `net/http`
  never proxies a loopback address, so its requests never reach faultkit.
  If your client bypasses the proxy, see
  [Faulting a local gateway's upstream](#faulting-a-local-gateways-upstream).
- **The 504 body is a generic JSON error** unless you set one:
  `{"error":{"message":"Gateway Timeout","type":"api_error"}}`, with
  `Content-Type: application/json`. If your client parses error bodies,
  set `response_body` (and `response_headers`) to what your gateway, or
  the proxy in front of it, really returns.
- **The slow variant still forwards a request your client gave up on.**
  faultkit doesn't notice a client that times out during the hold. If
  the run is still going when the 30 s run out, the request reaches your
  gateway: a real, possibly billed, call, and a duplicate if your client
  retried. The hang-then-504 variant never forwards anything.

**What it tests:**

- Client timeouts on gateway calls: is there one, and does it fire
  before your own request handler's?
- Retries on a timeout or a 504: how many, with what backoff, and
  whether a retry duplicates work
- Fallback to another route or model when the gateway doesn't answer
- What the user sees while waiting and after the app gives up

**YAML, slow** (save as `gateway-timeout-slow.yaml`):

```yaml
name: gateway-timeout-slow
description: Requests to the LLM gateway are held 30 s, then forwarded unchanged.
experiments:
  - name: "gateway slow"
    fault:
      delay_ms: 30000
    match:
      host: "gateway.example.com"   # replace with your gateway's host
      path: "/v1/chat/completions"  # replace with the path your app calls
    probability: 1.0
```

**YAML, hang then 504** (save as `gateway-timeout-504.yaml`):

```yaml
name: gateway-timeout-504
description: Requests to the LLM gateway hang for 60 s, then get a 504.
experiments:
  - name: "gateway hangs, then 504"
    fault:
      delay_ms: 60000
      http_status: 504
    match:
      host: "gateway.example.com"   # replace with your gateway's host
      path: "/v1/chat/completions"  # replace with the path your app calls
    probability: 1.0
```

Replace the host and path with your gateway's; `host` is required, a
match without it never fires. Pick `delay_ms` against your client's
timeout: longer than it tests the timeout path, shorter tests a slow
call that still succeeds. The limit is `300000` (5 minutes).
`probability: 1.0` holds every matched request, retries included, so the
run is repeatable.

**Run it:**

```bash
faultkit run --config gateway-timeout-504.yaml -- pytest tests/
```

Each held request is a fired event, counted when its hold starts. A
fired delay proves only that: the request reached faultkit and was held.
It counts even if your client gave up a moment later or the run ended
first (see [the delay fields](./yaml-schema.md#delay-level-neutral)), so
it doesn't prove your app handled anything. With exit `0` the report
says `invariant_proven_under_fault`, so the test itself has to check the
handling: that the call ended within your timeout, and that the user got
the fallback or an honest error. If no request matches (wrong host or
path) or none reaches faultkit (a client that bypasses the proxy), the
verdict is `invalid_evidence` and faultkit exits `3` unless the target
itself failed.

When the target exits, faultkit cuts every hold short and drops those
connections without forwarding them, so the run ends with the target,
not when the delay would have run out.

#### Faulting a local gateway's upstream

When the gateway runs on your machine (a self-hosted LiteLLM on
`localhost:4000`, say) and your app's client doesn't send loopback
traffic through the proxy, fault the gateway's own calls to the provider
instead. Start the gateway as a child of the target, so it inherits
faultkit's `HTTPS_PROXY` and CA variables, and set `host` and `path` in
one of the files above to your provider's (`api.openai.com` and
`/v1/chat/completions`, for example):

```bash
faultkit run --config gateway-timeout-504.yaml -- sh -c '
  your-gateway --port 4000 & gw=$!
  trap "kill $gw" EXIT
  sleep 5   # better: poll the gateway until it answers
  pytest tests/'
```

Now the gateway's upstream call is held, and whatever reaches your app
comes from the gateway: its own timeout error, the 504 passed on, or a
fallback. That tests the gateway's configuration together with your
app.

This works only if the gateway's HTTP client honors `HTTPS_PROXY` and
the CA bundle variables. faultkit hasn't been tried with specific
gateway products; if yours ignores them, nothing fires and the run exits
`3`. A gateway in a container or on another host isn't a child of the
target, so it can't be covered this way. Stop the gateway when the
target finishes, as the `trap` does: faultkit doesn't stop processes the
target leaves behind.

**What good behavior looks like:**

Your app gives up on the gateway at a timeout someone chose, shorter
than the timeout of whatever calls your app, retries at most once with
backoff, then falls back to another route or tells the user plainly
that the answer isn't coming.

**What bad behavior looks like:**

The app waits out its SDK's default timeout (often minutes), a request
handler times out first and the user gets a generic error, or the app
retries in a loop and piles more load on a gateway that is already slow.
A 504 body the client can't parse becomes a crash instead of a retry.

### `subagent-timeout`

An orchestrator agent hands work to a sub-agent over HTTP (an A2A agent,
an MCP server over streamable HTTP, or your own agent service), and the
call never comes back. Does the orchestrator give up at its own deadline
and carry on, or does the whole task hang with it?

In production this happens when a sub-agent is stuck in a long tool
loop, waiting on a slow dependency of its own, or deadlocked, or when a
response is lost without the connection closing.

The recipe holds each matched call for 5 minutes, the longest delay
faultkit allows, so the call doesn't return within the run. When the
orchestrator gives up and the target exits, faultkit cuts the hold short
and drops the connection. The sub-agent never receives the call.

**Limits:**

- **Needs a faultkit release newer than 0.1.3.** `delay_ms` and
  `match.method` are new; 0.1.3 and earlier ignore both without a
  warning, so the call gets an immediate `504` and so do the requests the
  recipe means to let through.
- **HTTP sub-agents only.** An MCP server over stdio, or a sub-agent
  your orchestrator runs as a subprocess, doesn't go through the proxy,
  so it isn't covered.
- **Forward-proxy mode only**, with the same conditions as
  [`gateway-timeout`](#gateway-timeout): the orchestrator's HTTP client
  must honor `HTTPS_PROXY` and trust faultkit's CA bundle, and a
  sub-agent on `localhost` is reached only by clients that send loopback
  traffic through the proxy. `--base-url` can't reach a sub-agent host:
  the run fails at start (exit `2`).
- **Calls are matched by method, host and path, not by body**, so
  faultkit can't tell one JSON-RPC method from another. An A2A client
  fetches the agent card with a `GET`, which `method: POST` lets
  through, so the first call held is the message itself. An MCP client
  over streamable HTTP sends every request as a `POST` to one endpoint,
  so the first call held is `initialize`: the session never starts, and
  you test the orchestrator's connect timeout rather than a tool-call
  timeout.
- **Every retry hangs too.** At `probability: 1.0` the orchestrator
  waits out its full timeout once per attempt.

**What it tests:**

- An orchestration ceiling: a deadline on each sub-agent call and on the
  task as a whole
- Cancellation: the orchestrator stops waiting instead of leaving the
  task stuck
- Degradation: finishing without the sub-agent's result, and saying so,
  or failing with a clear error
- Retries: a bounded number of attempts at a call that hangs

**YAML** (save as `subagent-timeout.yaml`):

```yaml
name: subagent-timeout
description: Calls to a sub-agent's HTTP endpoint never return within the run.
experiments:
  - name: "sub-agent call hangs"
    fault:
      delay_ms: 300000              # the maximum: 5 minutes
      # Keeps a held call from being forwarded to the sub-agent if the run
      # outlasts the delay (see below).
      http_status: 504
    match:
      host: "subagent.example.com"  # replace with the sub-agent's host
      path: "/a2a"                  # replace with its endpoint path, e.g. /mcp
      method: POST
    probability: 1.0
```

Replace the host and path with your sub-agent's endpoint; `host` is
required, a match without it never fires. Delete `method: POST` to hold
every request to the path, `GET`s included.

The `http_status: 504` only matters if the run outlasts the delay, for
example when the orchestrator has no deadline at all. By the time the 5
minutes run out, the proxy's own 5-minute limit for the request has
passed too, so the orchestrator sees the connection close instead of a
504, and the call still never reaches the sub-agent. Without a status,
the call would be forwarded to the sub-agent when the delay runs out,
even though no reply can reach the orchestrator any more.

**Run it:**

```bash
faultkit run --config subagent-timeout.yaml -- pytest tests/orchestrator/
```

Each held call is a fired event, counted when its hold starts. A fired
delay proves only that the call reached faultkit and was held. It counts
even if the orchestrator gave up a moment later (see
[the delay fields](./yaml-schema.md#delay-level-neutral)), so it doesn't
prove the orchestrator coped. With exit `0` the report says
`invariant_proven_under_fault`, so the test has to assert the ceiling
itself: that the task ended within your deadline, well under 5 minutes,
with the outcome you expect (a degraded answer or a clear error). If no
call matches or reaches faultkit, the verdict is `invalid_evidence` and
faultkit exits `3` unless the target itself failed.

Give the test runner a timeout above your ceiling too. An orchestrator
with no ceiling waits out the whole hold: its call fails only after 5
minutes, when the connection closes, and each retry waits another 5.

**What good behavior looks like:**

The orchestrator stops waiting on the sub-agent at a deadline someone
chose, well under 5 minutes. It cancels the call, then either finishes
the task without that result and says so, or fails with an error that
names the sub-agent.

**What bad behavior looks like:**

The orchestrator waits with no deadline, or with a library default of
many minutes, so the user waits too. It retries the hanging call again
and again, or it times out but still reports the task as complete with a
piece silently missing.

---

## Composing scenarios

Multiple experiments in one scenario:

```yaml
name: production-bad-day
description: Several LLM failures in the same run.
experiments:
  - name: "openai rate-limited 10% of the time"
    fault:
      http_status: 429
    match:
      host: "api.openai.com"
    probability: 0.1

  - name: "anthropic 503 on the fallback"
    fault:
      http_status: 503
    match:
      host: "api.anthropic.com"
    probability: 0.05
```

This is what production actually looks like — multiple low-probability
failures interacting in ways no single scenario tests. Every HTTP
experiment in the config is active for the whole run.

```bash
faultkit run --config production-bad-day.yaml -- pytest tests/
```

A run uses one injection mechanism, though. In a scenario that mixes
HTTP and syscall experiments, auto-mode runs the HTTP experiments
through the proxy and skips the syscall ones, and eBPF mode only ever
runs a scenario's first syscall experiment. Keep syscall faults in a
scenario of their own and run it separately.

---

## What's coming next

The following scenarios are planned. They don't ship yet, and
contributions are welcome:

All of them need syscall-level injection, through eBPF or the planned
LD_PRELOAD shim:

| Scenario | Mechanism | What it will test |
|---|---|---|
| `tool-call-flaky` | eBPF / shim | Truncated stdout, exit 0 — partial data, no error raised |
| `partial-tool-result` | eBPF / shim | Valid-but-incomplete tool result, exit 0 |
| `tool-slow` | eBPF / shim | Subprocess hangs for N seconds |
| `disk-full` | eBPF | `ENOSPC` after N bytes written |
| `fd-exhaustion` | eBPF | `EMFILE` after N open descriptors |
| `slow-dns` | eBPF / shim | `getaddrinfo` slow or `EAI_AGAIN` |

If you want to contribute one of these (or propose a new scenario),
see [CONTRIBUTING.md](../CONTRIBUTING.md).

---

## Writing your own scenarios

Every scenario in v0.1 is just YAML. You can write your own and run
them without modifying faultkit:

```bash
faultkit run --config my-scenario.yaml -- ./my-tests
```

The schema reference is in [docs/yaml-schema.md](./yaml-schema.md).
The fastest way to learn is to copy one of the built-in scenarios
and modify it.

If you write a scenario that catches a real bug in production code
— yours or an open-source project's — please share it. The scenario
library is one of faultkit's most valuable assets and contributions
here are high-leverage.
