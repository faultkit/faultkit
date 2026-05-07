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

v0.1 ships five scenarios. Three use HTTPS proxy injection (LLM and
gateway failures). Two use eBPF injection (Linux syscall-level
failures).

| Scenario | Mode | What it tests |
|---|---|---|
| `llm-api-degraded` | proxy | LLM provider returns 429 / 503 / timeout |
| `malformed-json-response` | proxy | LLM returns syntactically invalid JSON |
| `llm-streaming-cutoff` | proxy | Streaming response drops mid-token |
| `flaky-network` | eBPF | TCP connections drop with `ECONNRESET` |
| `tool-permission-denied` | eBPF | File operations fail with `EACCES` |

All five are free, open-source, and ship in the v0.1 binary. Run
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

## eBPF scenarios

These scenarios use faultkit's eBPF injector. Small kernel programs
hook syscall tracepoints and rewrite return values for processes
inside your target's PID tree. The target sees a real `ECONNRESET`,
a real `EACCES`, because that's what the kernel returned.

eBPF scenarios require Linux 5.8 or newer with `CAP_BPF` and
`CAP_NET_ADMIN` capabilities (or root). On other platforms,
`faultkit doctor` will report these scenarios as unavailable.

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

  - name: "tcp resets on database connections"
    fault:
      errno: ECONNRESET
    match:
      syscall: recvmsg
      port: 5432
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

| Scenario | Mode | What it will test |
|---|---|---|
| `tool-call-flaky` | eBPF | Subprocess `SIGPIPE` and short reads |
| `rag-corruption` | proxy | Vector DB returns shuffled or stale results |
| `context-window-squeeze` | proxy | Silent prompt truncation at the SDK boundary |
| `disk-full` | eBPF | `ENOSPC` after N bytes written |
| `slow-dns` | eBPF | `getaddrinfo` returns slowly or with `EAI_AGAIN` |

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
