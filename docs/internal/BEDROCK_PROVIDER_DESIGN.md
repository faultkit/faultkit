# Amazon Bedrock provider — design + task plan (internal)

> Internal design note. Companion to [AGENTIC_GAP_ANALYSIS.md](./AGENTIC_GAP_ANALYSIS.md)
> and [BASE_URL_INJECTION_DESIGN.md](./BASE_URL_INJECTION_DESIGN.md). This note
> designs faultkit's third LLM provider — Amazon Bedrock (`bedrock-runtime`) —
> and folds in two low-cost gap fixes surfaced during the design review. Bedrock
> was explicitly deferred out of v0.1 (see `V0.1_SPEC.md` §2); this is the v0.2-era
> plan to add it.

## Why Bedrock, why now

A large share of production Claude traffic does not go to `api.anthropic.com` —
it goes to Bedrock. faultkit's positioning is Anthropic-forward (the whole
`anthropic-*` scenario family), so the provider that actually fronts most Claude
usage in enterprises is the natural next one. The roadmap already points here
("Scenario packs … AWS SDK"), and the base-URL note calls Bedrock out as a later
provider. Adding it is the "new provider = new fixtures, not new scenarios"
invariant (`docs/scenarios.md`) put to its first real test against a provider
that is *not* a drop-in.

## The SigV4 insight (the decision that shapes everything)

Bedrock authenticates with **AWS SigV4**: the client signs a canonical request
over the host, path, headers, and a hash of the body. This interacts with
faultkit's two interception paths very differently:

- **Forward-proxy (MITM) mode — works.** The client still addresses the real
  host (`bedrock-runtime.<region>.amazonaws.com`); faultkit terminates TLS with
  its per-run CA but does **not** rewrite host or path. The SigV4 signature the
  client computed is therefore still valid. For a *non-fired* request faultkit
  forwards the bytes **unchanged** to the real upstream and the signature
  verifies; for a *fired* request faultkit synthesizes a response and never
  contacts upstream, so SigV4 is irrelevant. `boto3` and the AWS SDKs honor
  `HTTPS_PROXY` and `AWS_CA_BUNDLE`, so the existing env-injection path reaches
  them.
- **Base-URL / origin mode — does not work.** Base-URL mode rewrites the origin
  to faultkit's loopback. The SDK then signs for `127.0.0.1` (or the signature,
  computed for faultkit's host, is rejected by the real Bedrock upstream on
  pass-through). Re-signing would require faultkit to hold AWS credentials and
  implement SigV4 — a new dependency surface and a credential-handling burden we
  will not take on.

**Decision: Bedrock is supported in forward-proxy mode only.** Base-URL mode
explicitly does not list Bedrock among its providers; this is a documented
boundary, not a bug. Bedrock's `baseURLEnv` in `providerRegistry` is empty, and
base-URL injection skips any provider whose `baseURLEnv` is empty — so Bedrock is
never injected as a base URL. (`--provider bedrock` remains valid; it selects the
Bedrock *fixtures* in forward-proxy mode.)

## Non-goals (stay out of this iteration)

- Base-URL / origin mode for Bedrock (SigV4; see above).
- Re-signing forwarded requests with faultkit-held AWS credentials.
- `invoke` model-native body fixtures — the 200-body modes target the **Converse**
  shape first (stable, model-agnostic); model-native `invoke` bodies are a
  follow-up.
- SageMaker runtime, Bedrock Knowledge Bases / OpenSearch, DynamoDB, SQS — these
  map to *other* roadmap scenarios (RAG, `memory-write-failure`), not to this
  provider. Do not scaffold them here.
- A new dependency for AWS event-stream decoding — the streaming WP hand-rolls a
  minimal frame reader (stdlib only), consistent with `CLAUDE.md`.

## Model: what Bedrock changes vs. openai/anthropic

The existing providers are single-literal-host, static-bearer, JSON-body,
SSE-streaming. Bedrock differs on four axes; each is a small, contained change.

1. **Region-parameterized host.** `bedrock-runtime.<region>.amazonaws.com` (and
   the `-fips` variant). The provider registry's `upstream` is currently a single
   literal used both to forward and to match. Bedrock needs a **host pattern**
   (`bedrock-runtime.*.amazonaws.com`) for matching/attribution. The matcher
   already globs hosts; the seam that must learn patterns is
   `providersForHostGlobs` and `fixtures.Build`'s host dispatch.

2. **Endpoint shape.** Not `/v1/messages`. Bedrock runtime paths:
   - `POST /model/{modelId}/invoke` — non-streaming, model-native body.
   - `POST /model/{modelId}/invoke-with-response-stream` — streaming (event-stream).
   - `POST /model/{modelId}/converse` — unified, model-agnostic body.
   - `POST /model/{modelId}/converse-stream` — unified streaming (event-stream).
   `{modelId}` may be a URL-encoded ARN (provisioned throughput / inference
   profile), so fixtures match with a glob: `/model/*/converse`, `/model/*/invoke`.

3. **Error envelope (AWS JSON protocol).** Not the OpenAI or Anthropic shape.
   Body is `{"message":"..."}`; the exception type travels in the
   **`x-amzn-errortype`** response header (plus `x-amzn-RequestId`). faultkit
   needs a `bedrockErrorBody` synthesizer wired into `fixtures.Build`'s dispatch.

4. **Streaming framing.** Not SSE. `invoke-with-response-stream` / `converse-stream`
   return `Content-Type: application/vnd.amazon.eventstream` — a **binary** frame
   format (total-length, headers-length, prelude-CRC, headers, payload,
   message-CRC). `streaming.go` keys on `text/event-stream` and counts `data:`
   lines; neither applies. This is the single most expensive item and is deferred
   to its own WP.

## Body-shape nuance (why Converse first)

Bedrock has two 200 body shapes. `invoke` returns the **model-native** body (for
Claude, the Anthropic Messages shape). `converse` returns a **unified** shape:
`{"output":{"message":{...}},"stopReason":"...","usage":{...}}`. To keep "one
fixture per mode" clean and decoupled from the underlying model, the 200-body
modes (`malformed-json`, `malformed-tool-use`, `max-tokens-truncation`) target
the **Converse** shape first. Status-only modes (throttling, service-unavailable,
model-timeout, request-too-large) are body-shape-agnostic — they match
`/model/*/*` and only set status + error envelope — so they land first and carry
the most value for the least code.

## Failure-mode → Bedrock fixture mapping

| Mode (catalog id) | Bedrock fixture | Ships in |
|---|---|---|
| `rate-limited` | 429, `x-amzn-errortype: ThrottlingException` | WP-B |
| `request-too-large` | 400, `ValidationException` (oversized input — note: Anthropic uses 413, Bedrock 400; the per-provider fixture owns the status) | WP-B |
| `service-unavailable` *(new mode)* | 503, `ServiceUnavailableException` | WP-B |
| `model-timeout` *(new, Bedrock-only)* | `ModelTimeoutException` | WP-C |
| `max-tokens-truncation` | 200 Converse, `stopReason: "max_tokens"` | WP-C |
| `malformed-json` | 200 Converse, invalid JSON in text content | WP-C |
| `malformed-tool-use` | 200 Converse, schema-violating `toolUse.input` | WP-C |
| `streaming-cutoff` | event-stream, cut after N frames | WP-D (deferred) |
| `stream-error` | event-stream `ModelStreamErrorException` mid-stream | WP-D (deferred) |

Modes with no cross-provider equivalent (`model-timeout`, `stream-error` on
Bedrock) ship as their own single-provider scenarios, exactly as the
`anthropic-*` family does — they are just modes with a single Bedrock fixture.

**Exact HTTP status per exception is confirmed against a live Bedrock response
during WP-B/WP-C, not hard-coded from memory.** The `x-amzn-errortype` header is
the authoritative signal; the status codes above are the expected values to
verify, not assumptions to ship blind.

## Work packages

Each WP is an independent branch off `main` and lands as its own PR (per
`CLAUDE.md`: never commit to `main`, one logical change per commit). Every WP runs
`make lint test` and, at completion, `make sec` (gosec + nilaway, zero findings).
No new dependencies in any WP.

### WP-A · `refactor/fixture-host-registry` — prerequisite (easy)

Generalize the two seams that hard-code literal hosts so a pattern-matched
provider can slot in without touching openai/anthropic behavior.

- Turn `fixtures.Build`'s `switch host` (`internal/inject/proxy/fixtures/fixtures.go`)
  into a small ordered table of `{hostGlob, errorBody}` so a new provider adds a
  row, not a case.
- Teach `providersForHostGlobs` / provider `upstream` matching to accept a host
  **pattern** (reuse `globMatch`), so a regional Bedrock host resolves to the
  `bedrock` provider.
- *Verify:* existing openai/anthropic fixture + matcher tests pass unchanged
  (regression guard); a new unit test resolves a glob-host provider.

### WP-B · `feat/bedrock-provider-core` — provider + status modes

- Add the `bedrock` entry to `providerRegistry` (`providers.go`): host pattern
  `bedrock-runtime.*.amazonaws.com`, endpoint prefixes, `baseURLEnv` empty
  (forward-proxy only — documented).
- Add `internal/inject/proxy/fixtures/bedrock.go`: `bedrockErrorBody(status)`
  producing `{"message":...}` + the `x-amzn-errortype` header; wire it into the
  WP-A dispatch table.
- Catalog fixtures for `rate-limited`, `request-too-large`, and the new
  `service-unavailable` mode, all Bedrock-shaped.
- Confirm forward-proxy pass-through leaves signed requests byte-identical
  (SigV4 stays valid); never log `Authorization` / `X-Amz-*`.
- *Verify:* unit test asserts Bedrock 429 envelope + header; matcher fires a
  Bedrock scenario against a regional host; a pass-through test shows the
  forwarded request unmodified.

### WP-C · `feat/bedrock-scenarios-body` — Converse-shape + Bedrock-distinctive

- Converse-shape 200-body fixtures: `max-tokens-truncation`, `malformed-json`,
  `malformed-tool-use`.
- Bedrock-only scenarios shipped like the `anthropic-*` family:
  `bedrock-model-timeout` (408 `ModelTimeoutException`),
  `bedrock-service-unavailable` (503) as a named scenario over the WP-B mode.
- Docs: add the Bedrock rows to `docs/scenarios.md` under
  [Failure modes and providers], note forward-proxy-only.
- *Verify:* each new fixture has a test asserting the exact body/stop-reason; the
  scenarios appear in `faultkit scenario list`.

### WP-D · `feat/bedrock-eventstream` — streaming (deferred, separate)

- Hand-rolled minimal AWS event-stream reader: read the 4-byte total-length
  prefix to walk frame boundaries (no CRC validation needed to *count* frames);
  forward N frames, then close for `streaming-cutoff`; inject a
  `ModelStreamErrorException` event frame mid-stream for `stream-error`.
- Dispatch on `Content-Type: application/vnd.amazon.eventstream` alongside the
  existing SSE path in the response handler.
- *Verify:* a frame-boundary unit test on captured event-stream bytes; cutoff
  fires after the configured frame count; error frame parses as an error, not a
  clean end.

### WP-E · `fix/stream-cutoff-token-fidelity` — easy gap (independent)

`streaming.go` counts every `data:` line as a "token", but a `data:` line is an
SSE *event*, not a token (`message_start`, `content_block_start`, … all carry
`data:`). Count only content-bearing deltas (OpenAI `choices[].delta.content`,
Anthropic `content_block_delta`) and correct the `stream_cutoff_tokens` field
docs to say "content deltas" (or rename with a back-compat alias — decide in the
plan, schema changes are a public-API commitment per `CLAUDE.md`).

- *Verify:* a cutoff configured at N now cuts after N content deltas, not N
  events, on both OpenAI and Anthropic captured streams.

### WP-F · `feat/check-provider-list` — easy gap (independent)

`faultkit check` reports modes but not providers. List the registered providers
and, per provider, the failure modes with a fixture, so a user can *see* Bedrock
was added and what fires against it.

- *Verify:* `faultkit check` output includes `bedrock` and its modes; exit-code
  behavior unchanged.

## Dependencies

```
WP-A ──▶ WP-B ──▶ WP-C
                └▶ WP-D (deferred)
WP-E (independent)
WP-F (independent)
```

Minimum shippable Bedrock slice that carries real value: **WP-A + WP-B**
(provider + status-based failure modes over forward-proxy, pass-through intact).
WP-C completes the 200-body Claude-on-Bedrock story; WP-D adds streaming; WP-E/WP-F
are independent quality fixes that can land in any order.

## Constraints (from CLAUDE.md, restated because they bind here)

- **No new dependencies.** Event-stream decoding is hand-rolled stdlib.
- **A new vendor fixture is a public-API commitment.** Each Bedrock fixture ships
  with a test and a `docs/scenarios.md` entry.
- **Never log secrets.** Bedrock forwards `Authorization` / `X-Amz-Security-Token`;
  redact them from any verbose/debug output.
- **`make sec` is the phase-completion gate.** gosec + nilaway zero findings per WP.
- **No Pro-aware code.** Provider expansion is generic; nothing here knows Pro exists.

## Validation gate

Before calling Bedrock "supported", confirm end-to-end **interception + fault
firing** in forward-proxy mode on:

1. `boto3` `bedrock-runtime` client, `converse` — 429 ThrottlingException fires;
   non-fired request passes through to a stub upstream unchanged (SigV4 valid).
2. `boto3` streaming `converse-stream` — pass-through works (WP-B); cutoff fires
   (WP-D).
3. A Bedrock-fronted agent test wrapped by `faultkit run` exits non-zero on the
   injected fault.

Record results in the WP-B / WP-D PRs.
