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
provider's own, so a streamed response streams normally once it starts,
and a non-streamed one arrives whole; only its start is late. The fault
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
  Pinecone's Python SDK documents its proxy settings as the `proxy_url` and
  `ssl_ca_certs` arguments — if your client doesn't pick up the environment,
  pass `os.environ["HTTPS_PROXY"]` and `os.environ["SSL_CERT_FILE"]` there.
  If no query reaches faultkit, nothing fires, and zero fired faults never
  produce a passing run: faultkit exits `3` if the target otherwise
  succeeded, or `1` if the target itself failed — with verdict
  `invalid_evidence` either way.
- **REST only.** gRPC clients (Pinecone's `GrpcIndex`, Qdrant on port 6334
  or with `prefer_grpc=True`) aren't faulted. If they honour
  `HTTPS_PROXY`/`grpc_proxy`, they fail to connect at all: the proxy only
  offers HTTP/1.1, and gRPC needs HTTP/2, so the TLS handshake ends with
  `no_application_protocol`.
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

Sequenced by the capability each needs (new LLM fixtures first, then a
latency/hang primitive, then the eBPF/shim subprocess track):

| Scenario | Mechanism | What it will test |
|---|---|---|
| `gateway-timeout` | proxy | LiteLLM / Portkey / gateway returns slow or hangs |
| `mcp-tool-schema-mismatch` | proxy | MCP result doesn't match its declared schema |
| `subagent-timeout` | proxy | A subagent tool call never returns |
| `memory-write-failure` | proxy / eBPF | Agent state write silently fails; next step reads stale |
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
