# Base-URL injection — design + task plan (internal)

> Internal design note. Companion to [AGENTIC_GAP_ANALYSIS.md](./AGENTIC_GAP_ANALYSIS.md),
> which establishes *why* this is the single highest-leverage v0.1-era change:
> faultkit today reaches a target's LLM traffic only if the client honors
> `HTTPS_PROXY`, and a large slice of real agentic tools (Node/SDK/subprocess/
> containerized) do not. Base-URL injection is the additive interception path
> that fixes that for the SDK majority.

## Goal

Let faultkit intercept and inject faults into LLM traffic by pointing the
client's SDK **directly at faultkit** via provider base-URL environment
variables (`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, …), in addition to the
existing `HTTPS_PROXY` forward-proxy path. SDKs honor base-URL env even when
they ignore proxy env, so this works where the forward proxy silently doesn't.

Non-goals (stay v0.2): cgroup/transparent eBPF redirect for clients that honor
*no* env at all; in-process MCP tool-call faults.

## Model: two coexisting interception paths

1. **Forward-proxy mode (existing, unchanged).** Client honors `HTTPS_PROXY` →
   CONNECT → per-run-CA MITM → matcher/faulter. Default; untouched.
2. **Base-URL / origin mode (new, additive).** faultkit injects per-provider
   base-URL env pointing at a faultkit-served origin endpoint. The client
   connects **directly** to faultkit as if it were the API. faultkit applies
   the same matcher/faulter, then either synthesizes a fault or forwards to the
   real upstream. No `HTTPS_PROXY` dependency; in the HTTP variant, no CA/MITM.

The two share the matcher, faulter, streaming, scenario schema, and exit codes.
Base-URL mode is purely additive plumbing around those.

## Key design decisions (and open questions)

- **Provider registry.** A small table: provider id → `{ upstream (scheme+host),
  base-URL env keys, path prefix }`. Seed `openai` (api.openai.com,
  `OPENAI_BASE_URL`+`OPENAI_API_BASE`) and `anthropic` (api.anthropic.com,
  `ANTHROPIC_BASE_URL`). Extensible to bedrock/vertex/litellm later.
- **Provider routing on one listener (recommended): path prefix.** Inject
  `OPENAI_BASE_URL=http://127.0.0.1:<port>/__fk/openai`; the SDK appends
  `/v1/...`, so faultkit sees `/__fk/openai/v1/...`, identifies the provider
  from the prefix, and strips it. (Alternative: one loopback port per provider
  — simpler routing, more sockets. Decide in T4.)
- **Matcher host-rewrite.** In origin mode the request's literal Host is
  faultkit's loopback, but scenarios match the *real* host (`api.anthropic.com`).
  faultkit rewrites the effective host to the registry upstream before matching,
  so existing host/path scenarios fire unchanged.
- **TLS: ship HTTP origin first.** `http://127.0.0.1:...` base URL → no TLS, no
  CA dance (the stated simplification). Most SDKs accept an http base URL for a
  local endpoint. **Open Q:** add an HTTPS origin variant (reuse per-run CA +
  CA-env) for clients that refuse http base URLs — schedule as T9, not P1.
- **Pass-through.** Non-fired requests forward to the real upstream over real
  TLS, preserving method, path, headers (incl. the client's `Authorization`),
  and body/stream. **Security:** never log the forwarded auth header/body.
- **Zero-config provider selection.** Derive the providers to inject from the
  scenario's matched hosts ∩ registry (so `llm-api-degraded` auto-targets
  openai+anthropic). A flag toggles base-URL mode on; no per-run provider list
  needed. **Open Q:** flag name (`--via=base-url` vs `--inject=base-url`) and
  whether it composes with `--mode`.
- **No-op detection.** Count requests that traverse faultkit; if zero at exit,
  emit a clear "target never connected to faultkit" message and use exit 3
  (closes gap F).

## Task plan (maximal but sensible)

Phased; each task is independently implementable and has a verify check.

### Phase 1 — core HTTP origin path (makes Node/SDK + Shannon-shape targets work)

- **T1 · Provider registry.** New internal type + table (id → upstream, env
  keys, prefix); seed openai + anthropic. *Files:* `internal/inject/proxy/` (new
  `providers.go`). *Verify:* unit test resolves env keys + upstream per id.
- **T2 · CLI surface + provider derivation.** Add the base-URL toggle to `run`;
  derive target providers = scenario match-hosts ∩ registry; default off; print
  the chosen providers. *Files:* `internal/cli/run.go`. *Verify:* `--help` shows
  it; enabling on `llm-api-degraded` reports openai+anthropic. Don't change exit
  codes.
- **T3 · Base-URL env injection.** When base-URL mode on, inject each provider's
  base-URL env keys = faultkit origin URL; keep the forward-proxy env path
  intact and mutually exclusive per run. *Files:* `internal/inject/proxy/proxy.go`.
  *Verify:* injected env contains the expected `*_BASE_URL` values.
- **T4 · Origin listener + provider routing.** Serve direct (non-CONNECT)
  requests; identify provider from the path prefix (or port); strip the prefix.
  *Files:* `internal/inject/proxy/server.go`. *Verify:* request to
  `/__fk/anthropic/v1/messages` is attributed to anthropic with path
  `/v1/messages`.
- **T5 · Matcher host-rewrite.** Feed the matcher the registry upstream host so
  existing host/path globs match origin-mode requests. *Files:*
  `internal/inject/proxy/matcher.go` (or the server seam). *Verify:* scenario
  `host: api.anthropic.com` fires on an origin-mode request.
- **T6 · Pass-through forwarding.** Forward non-fired requests to the real
  upstream over TLS, preserving method/path/headers/body/stream; return the real
  response. Redact secrets from logs. *Files:* `internal/inject/proxy/server.go`.
  *Verify:* a pass-through request reaches a stub upstream unchanged.
- **T7 · Fault synthesis in origin mode.** Reuse the faulter to synthesize
  status/body for fired requests on the origin path. *Files:* faulter wiring in
  `server.go`. *Verify:* a 429 (llm-api-degraded) is synthesized for an
  origin-mode openai request.

### Phase 2 — robustness + Claude parity

- **T8 · Streaming (SSE) in origin mode.** Ensure `llm-streaming-cutoff` and
  pass-through SSE work through the origin path (reuse streaming logic). *Files:*
  `internal/inject/proxy/streaming.go`. *Verify:* streaming-cutoff fires on an
  origin-mode request.
- **T9 · HTTPS origin variant (optional).** Serve origin over TLS reusing the
  per-run CA + inject CA-env, for clients that require an https base URL. *Files:*
  `ca.go`, `server.go`, `proxy.go`. *Verify:* an https-base-URL client is
  intercepted. (Skippable if http suffices.)
- **T10 · No-op diagnostics.** Count requests through faultkit; warn clearly +
  exit 3 when zero ("target never connected — base URL not honored?"). *Files:*
  `internal/report/`, `internal/cli/run.go`. *Verify:* wrapping a no-traffic
  command warns and exits 3.
- **T11 · Anthropic scenario parity.** Add `api.anthropic.com/v1/messages`
  variants for `streaming-cutoff` and `malformed-json` (Anthropic SSE event
  shape). *Files:* `internal/scenario/builtin/*.yaml` + fixtures. *Verify:* both
  fire against anthropic.

### Phase 3 — validation + docs

- **T12 · Tests + Node example.** Unit (registry, host-rewrite, env injection);
  integration e2e: a Node SDK client with base-URL env → faultkit intercepts +
  fires. Add a Node example under `examples/`. *Files:* `*_test.go`,
  `test/integration/`, `examples/`. *Verify:* `make test` + the new integration
  test pass.
- **T13 · Docs.** User docs: when to use proxy vs base-URL, CLI flags, provider
  list, container usage (`docker run -e ANTHROPIC_BASE_URL=…`). Reconcile README
  claims (it already lists multi-provider). *Files:* `docs/`, `README.md`.
  *Verify:* links/examples accurate.
- **T14 · Validation gate.** Run the 3-stack gate from the gap analysis:
  (1) python pytest+openai, (2) node ai-sdk/LangChain.js, (3) subprocess/
  container (Shannon-shape). Confirm interception + firing on each; record
  results. *Verify:* (2) and (3) now pass (they don't today).

## Dependencies

T1 → T2,T3,T4. T4 → T5 → T6,T7. T7 → T8. (T9 parallel.) T10 anytime after T4.
T11 independent. T12 after Phase 1. T13/T14 last.

Minimum shippable slice that closes most of the gap: **T1–T7 + T10** (HTTP
origin, openai+anthropic, no-op diagnostics). T8/T11 make the Claude streaming
story complete; T9 covers https-only clients; T12–T14 prove it.
