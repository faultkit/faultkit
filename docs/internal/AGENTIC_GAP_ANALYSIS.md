# Agentic readiness — gap analysis (internal note)

> Internal engineering note. Not for publication as-is: it is candid about
> faultkit's current limits and is based on evaluating a third party's code.
> The evaluated tool is anonymized here ("the eval target").

## What we evaluated

faultkit was assessed against **the eval target**: a real-world, production
agentic product (Node/TypeScript, pnpm monorepo) that runs a multi-agent
pipeline. Its shape is representative of a large class of modern agentic tools:

- LLM traffic is **Anthropic/Claude only**, and is emitted by a **child
  subprocess** (an agent CLI), not by the host process. It streams (SSE).
- The host process spawns the work in a **separate Docker container per run**.
- Outbound HTTP uses **SDKs / a subprocess**, not raw env-respecting clients;
  the subprocess receives an **env allowlist** that omits proxy variables.
- Tools run as **in-process MCP servers** plus a few **PATH subprocess CLIs**.
- **No unit/integration test suite** — the realistic target is a live run.
- Heavy resilience already present (durable-workflow retries, rate-limit
  recovery, retryable-error classification on 429/5xx/ECONNRESET).

This is exactly the failure surface faultkit aims at — which makes it a good
stress test of whether faultkit can actually reach a real agentic tool.

## What faultkit does right

- **The scenario catalog matches the real risk.** 429/503 with `Retry-After`
  (exercises rate-limit recovery), streaming cutoff (long SSE activities),
  malformed JSON (structured output) — these are the agentic failures.
- **`llm-api-degraded` already matches `api.anthropic.com/v1/*`**, so the
  rate-limit scenario applies to Claude traffic directly.
- **The architecture is the right shape:** generic host/path glob + custom
  YAML (provider-agnostic), two mechanisms (proxy + eBPF), PID-tree/env
  propagation, deterministic exit codes. HTTP/1.1 proxy matches the Anthropic
  SDK's HTTP/1.1-class transport.

## Gaps (why it doesn't reach the eval target out of the box)

**A. Interception relies solely on `HTTPS_PROXY` env — the Achilles' heel.**
faultkit injects only `HTTPS_PROXY`/`HTTP_PROXY` + CA-path env
(`internal/inject/proxy/proxy.go`). The eval target's LLM calls happen in a
child subprocess whose env allowlist **strips `HTTPS_PROXY`**, so the injected
env never reaches the process making the calls. More broadly, Node's global
`fetch`/undici (most of the JS agentic ecosystem) **ignore `HTTPS_PROXY` by
default**. Result: faultkit silently intercepts nothing.

**B. No reach into spawned subprocesses' separate containers.** Wrapping the
host command does not cross a `docker run` boundary (new PID namespace, env not
forwarded, eBPF PID-tree does not follow). Agentic tools increasingly sandbox
work in containers.

**C. Scenario coverage is OpenAI-centric.** `streaming-cutoff` and
`malformed-json-response` match only `api.openai.com/v1/chat/completions`; they
do **not** match Anthropic `/v1/messages`. The agentic world is Claude-heavy,
so the two marquee scenarios don't fire on Anthropic-only tools without a
custom YAML. (`llm-api-degraded` is the exception — it covers Anthropic.)

**D. The in-process tool-call layer is unreachable.** In-process MCP tools have
no network/syscall boundary, so faultkit can't touch them. "A tool call fails
mid-loop" is the most agentic-specific failure, and it lives below faultkit's
network+syscall reach. The deferred `tool-call-flaky` (subprocess SIGPIPE) only
helps stdio/subprocess tools, not in-process MCP.

**E. No test suite to wrap weakens the CI/exit-code value.** faultkit is
strongest wrapping a test command (`faultkit run -- pytest`). Tools without a
suite force you to wrap a long, costly, non-deterministic live run.

**F. Silent no-op risk.** Because interception is fragile, faultkit must loudly
report when **zero requests traversed the proxy**, or users get a false green.
Exit code 3 + `--verbose` help, but proxy mode should explicitly distinguish
"target never used the proxy."

## The one v0.1-era change that closes most of the gap: base-URL injection

**Highest leverage, modest effort:** in addition to `HTTPS_PROXY`, let faultkit
inject **provider base-URL env vars** that point the client's SDK directly at
the faultkit proxy — e.g. `OPENAI_BASE_URL`, `OPENAI_API_BASE`,
`ANTHROPIC_BASE_URL`, and friends.

Why this closes most of gap **A** (and helps **B**):

- **SDKs read base-URL env even when they ignore proxy env.** The official
  OpenAI and Anthropic SDKs (and most gateways/LiteLLM/OpenRouter setups)
  honor a base-URL override. So this works for the SDK majority — including
  the eval target, where `ANTHROPIC_BASE_URL` is a first-class, supported
  config that *is* forwarded into the subprocess.
- **No `HTTPS_PROXY` dependency and no CA/MITM needed.** In base-URL mode the
  client connects **directly** to the faultkit endpoint as if it were the API
  origin, so we sidestep both the proxy-env problem and the per-run-CA trust
  dance (`SSL_CERT_FILE`/`NODE_EXTRA_CA_CERTS`/…). Simpler and more robust.
- **Container reach becomes trivial:** the same base-URL env is easy to pass to
  a spawned container (`docker run -e ANTHROPIC_BASE_URL=…`), where injecting a
  proxy + CA across the boundary is not.

### Sketch of the work

1. Add a small set of base-URL env keys to the injector
   (`internal/inject/proxy/proxy.go`), populated with the proxy's listen URL,
   gated behind a flag/config (e.g. `--inject-base-url` or per-scenario), so
   default behavior (proxy env) is unchanged.
2. Teach the proxy to serve as an **origin/reverse endpoint** for base-URL
   mode: accept the client's direct request, map it to the real upstream
   (per-provider: anthropic↔api.anthropic.com, openai↔api.openai.com), apply
   the same matcher/faulter it already uses, and pass through otherwise.
   (This is the only non-trivial part; the matcher, fault synthesis, and
   streaming logic already exist.)
3. Reuse exit codes and reporting unchanged.

The existing forward-proxy/CONNECT path stays for tools that *do* honor proxy
env; base-URL mode is the additive path for the (large) set that don't.

## Secondary priorities (after base-URL injection)

- **Anthropic scenario parity:** add `api.anthropic.com/v1/messages` variants
  of `streaming-cutoff` and `malformed-json` (Anthropic SSE event shape differs
  from OpenAI's). Closes gap **C** for the Claude-heavy agentic world.
- **Loud "0 requests intercepted" diagnostic** in proxy mode. Closes gap **F**;
  cheap and high-trust.
- **Container guidance:** document "run faultkit (or pass the injected env)
  inside the sandbox container." Partially closes **B**.
- **Tool-call faults (v0.2):** the deferred `tool-call-flaky` for
  stdio/subprocess + HTTP-MCP tools. In-process MCP stays out of reach without
  an SDK-level hook — acknowledge the boundary. Addresses gap **D**.
- **cgroup/transparent eBPF redirect (v0.2, already planned):** the catch-all
  for clients that ignore *all* env (raw fetch with no base-URL). Fully closes
  **A** for the remainder.

## Validation gate ("be sure it works for real agentic tools")

Before claiming agentic readiness, confirm end-to-end **interception + fault
firing** on three representative stacks:

1. Python `pytest` + `openai` SDK — the current example. **Passes today.**
2. Node `ai-sdk` / LangChain.js (raw-ish fetch, ignores proxy env).
3. SDK/subprocess + container (the eval target's shape).

Today only (1) passes cleanly. **Base-URL injection is what makes (2) and (3)
pass**, which is why it is the single most valuable v0.1-era addition.
