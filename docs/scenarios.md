# Scenarios

faultkit ships scenarios that map to real production failure modes in
AI agent and backend systems. Each scenario is a YAML recipe that
faultkit replays deterministically against your application, so you
can verify your code survives failures you've written handlers for
but never actually seen.

This document covers every scenario shipping in v0.1. For roadmap
scenarios (v0.2 and beyond), see the bottom of this page.

---

## At a glance

v0.1 ships twelve scenarios. Ten use HTTPS proxy injection (LLM
failures); two use eBPF injection (Linux syscall-level failures).

**Cross-provider LLM** — fire against OpenAI and Anthropic; narrow with
`--provider` (see [Failure modes and providers](#failure-modes-and-providers)):

| Scenario | Mode | What it tests |
|---|---|---|
| `llm-api-degraded` | proxy | LLM provider returns 429 / 503 / timeout |
| `malformed-json-response` | proxy | LLM returns syntactically invalid JSON |
| `malformed-tool-use` | proxy | Tool call with malformed / schema-violating arguments |
| `max-tokens-truncation` | proxy | Truncated 200 the agent treats as complete |
| `llm-streaming-cutoff` | proxy | Streaming response drops mid-token |

**Anthropic-specific** — failures Claude surfaces with no OpenAI equivalent:

| Scenario | Mode | What it tests |
|---|---|---|
| `anthropic-overloaded` | proxy | HTTP 529 `overloaded_error` |
| `anthropic-stream-error` | proxy | SSE `error` event mid-stream, no `message_stop` |
| `anthropic-tool-use-cutoff` | proxy | `tool_use` truncated by `max_tokens` |
| `anthropic-refusal` | proxy | 200 with `stop_reason: "refusal"` |
| `anthropic-request-too-large` | proxy | HTTP 413 `request_too_large` |

**Syscall-level** — Linux, eBPF:

| Scenario | Mode | What it tests |
|---|---|---|
| `flaky-network` | eBPF | TCP connections drop with `ECONNRESET` |
| `tool-permission-denied` | eBPF | File operations fail with `EACCES` |

All twelve are free, open-source, and ship in the v0.1 binary. Run
`faultkit scenario list` to see what's available on your installation.

---

## How scenarios work

Every scenario has the same shape:

```yaml
name: my-scenario              # required, kebab-case identifier
description: |                  # optional, shown in `scenario list`
  One-line description.

requires:                      # optional, platform constraints
  platform: linux              # one of: linux, darwin, any
  kernel_min: "5.8"            # only relevant for eBPF scenarios

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
first match wins. Probability is independent per request: a `0.2`
probability fires roughly 20% of matching requests, with the dice
rolled fresh each time.

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
    probability: 0.2          # no `provider:` → fires for OpenAI and Anthropic
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

---

## Proxy scenarios

These scenarios use faultkit's HTTPS proxy. Your application connects
to the LLM provider as normal, but faultkit intercepts the connection,
terminates TLS with a per-run certificate, and rewrites the response.
From your application's perspective, the failure is indistinguishable
from a real provider failure — because at the HTTP layer, that's what
it is.

### `llm-api-degraded`

**The marquee scenario.** Your LLM provider returns rate limits,
service errors, or timeouts under load. Your retry logic kicks in.
Did it kick in correctly? Did the retry preserve reasoning state? Did
it back off? Did it give up gracefully?

Real production failure mode: OpenAI rate limits during traffic
spikes, Anthropic 503s during model rollouts, provider-side
provisioning issues, network path degradation between your VPC and the
provider.

**What it tests:**

- Retry logic with backoff and jitter
- Per-tenant rate limiting and circuit breakers
- Fallback provider activation (if you have one)
- User-facing degradation messaging
- Cost controls (does your code retry forever?)

**YAML:**

```yaml
name: llm-api-degraded
description: LLM provider returns 429/503/timeout under load.
experiments:
  - name: "openai rate-limited"
    fault:
      http_status: 429
      response_headers:
        Retry-After: "30"
    match:
      host: "api.openai.com"
      path: "/v1/chat/completions"
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
N tokens, then close the connection without sending the terminator.

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

## eBPF scenarios

These scenarios use faultkit's eBPF injector. Small kernel programs
hook syscall tracepoints and rewrite return values for processes
inside your target's PID tree. The target sees a real `ECONNRESET`,
a real `EACCES`, because that's what the kernel returned.

eBPF scenarios require Linux 5.8 or newer with `CAP_BPF` and
`CAP_NET_ADMIN` capabilities (or root). On other platforms,
`faultkit check` will report these scenarios as unavailable.

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
description: Multiple failures happening simultaneously.
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

  - name: "tcp resets on backend recv"
    fault:
      errno: ECONNRESET
    match:
      syscall: recvmsg
    probability: 0.02
```

This is what production actually looks like — multiple low-probability
failures interacting in ways no single scenario tests. faultkit
applies them all simultaneously when you run with this config.

```bash
faultkit run --config production-bad-day.yaml -- pytest tests/
```

---

## What's coming next

The following scenarios are planned for v0.2. They aren't shipping in
v0.1 but are tracked as open issues and welcome contribution:

Sequenced by the capability each needs (new LLM fixtures first, then a
latency/hang primitive, then RAG providers, then the eBPF/shim subprocess
track):

| Scenario | Mechanism | What it will test |
|---|---|---|
| `llm-empty-response` | proxy | 200 with empty / null content (guardrail-triggered) |
| `llm-slow-first-token` | proxy | 8–15s before the first streamed token |
| `context-window-overflow` | proxy | Outbound prompt silently truncated at the SDK boundary |
| `gateway-timeout` | proxy | LiteLLM / Portkey / gateway returns slow or hangs |
| `rag-stale-results` | proxy | Vector DB returns stale or deleted-document results |
| `embeddings-degraded` | proxy | Embeddings endpoint returns 5xx intermittently |
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
