# Amazon Bedrock provider — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Amazon Bedrock (`bedrock-runtime`) as faultkit's third LLM provider in forward-proxy mode, plus two low-cost quality fixes surfaced during design review.

**Architecture:** Bedrock reuses the existing forward-proxy MITM path unchanged. SigV4 stays valid because host/path are never rewritten, so non-fired requests pass through to real Bedrock and fired requests are synthesized. Bedrock is added as a provider-registry entry plus a per-provider fixture set (the "one scenario, N fixtures" invariant); existing failure-mode scenarios auto-fan-out to it. Base-URL mode explicitly excludes Bedrock (SigV4 breaks origin rewrite).

**Tech Stack:** Go 1.22+, `github.com/google/martian/v3` (proxy), `gopkg.in/yaml.v3` (scenarios), stdlib `encoding/json`. No new dependencies.

**Design source:** `docs/internal/BEDROCK_PROVIDER_DESIGN.md`.

## Global Constraints

Every task's requirements implicitly include these (from `CLAUDE.md` + the design note):

- **No new dependencies.** Not even test-only. Event-stream decoding (WP-D) is hand-rolled stdlib.
- **A new vendor fixture is a public-API commitment.** Each fixture ships with a test and a `docs/scenarios.md` entry.
- **Never log secrets.** Bedrock forwards `Authorization` / `X-Amz-Security-Token`; keep them out of any verbose/debug output.
- **Gates per WP:** `make lint test` throughout; `make sec` (gosec `@v2.26.1` + nilaway pinned, both zero findings) before a WP is done.
- **No Pro-aware code.** Provider expansion is generic; nothing here knows Pro exists.
- **Tests are black-box (`package foo_test`) by default**; match the sibling test file's package clause where it already differs.
- **Never commit to `main` directly. PR-only.** One logical change per commit; imperative subject < 72 chars.
- **Commit trailer** on every commit: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **CLI/exit-code/schema are frozen contracts.** `stream_cutoff_tokens` is a locked YAML field name — do not rename it.

## Branch model

Each work package is its own branch off `main` and lands as its own PR. Start every WP with:

```bash
git checkout main && git pull --ff-only
git checkout -b <branch>
```

| WP | Branch | Depends on |
|---|---|---|
| A | `refactor/fixture-host-registry` | — |
| B | `feat/bedrock-provider-core` | A |
| C | `feat/bedrock-scenarios-body` | B |
| D | `feat/bedrock-eventstream` (deferred) | B |
| E | `fix/stream-cutoff-token-fidelity` | — |
| F | `feat/check-provider-list` | — |

WP-A → WP-B → WP-C is the critical path. WP-E and WP-F are independent and may land in any order. WP-D is deferred (needs captured event-stream bytes first; see its section).

## File structure

**Modified:**
- `internal/inject/proxy/fixtures/fixtures.go` — Build host dispatch: `switch` → ordered table (WP-A); add bedrock template row (WP-B).
- `internal/inject/proxy/providers.go` — `upstream` doc allows a host glob; `providersForHostGlobs` skips empty-`baseURLEnv` providers (WP-A); add `bedrock` registry entry (WP-B).
- `internal/inject/proxy/fixtures/catalog.go` — add `bedrock` fixtures to shared modes (WP-B/C) and new bedrock-only modes (WP-C).
- `internal/inject/proxy/streaming.go` — count content deltas, not raw `data:` events (WP-E).
- `internal/scenario/builtin/llm_api_degraded.yaml` — description mentions Bedrock (WP-B).
- `internal/cli/check.go` — list providers + their modes (WP-F).
- `docs/scenarios.md` — Bedrock rows (WP-B/C).

**Created:**
- `internal/inject/proxy/fixtures/bedrock.go` — `bedrockErrorBody` + Converse-shape body constants (WP-B/C).
- `internal/inject/proxy/fixtures/bedrock_test.go` — fixture tests (WP-B/C).
- `internal/scenario/builtin/bedrock_model_timeout.yaml`, `bedrock_service_unavailable.yaml` — bedrock-only scenarios (WP-C).

---

## WP-A · `refactor/fixture-host-registry` (prerequisite, behavior-preserving)

Generalize the two seams that hard-code literal hosts so a pattern-matched, forward-proxy-only provider can slot in. No behavior change for openai/anthropic — existing tests are the regression guard.

**Files:**
- Modify: `internal/inject/proxy/fixtures/fixtures.go`
- Modify: `internal/inject/proxy/providers.go`
- Test: `internal/inject/proxy/fixtures/fixtures_test.go` (add case), `internal/inject/proxy/providers_test.go` (add case)

**Interfaces:**
- Produces: `fixtures.Build(host string, fault faulttypes.Fault) fixtures.Synthetic` — unchanged signature; now dispatches via an ordered `vendorTemplates` table. Later tasks add a row.
- Produces: `providersForHostGlobs([]string) []provider` — now skips providers whose `baseURLEnv` is empty.

- [ ] **Step 1: Write a failing test that a bedrock-shaped host would fall through to generic today, and lock openai/anthropic dispatch**

Add to `internal/inject/proxy/fixtures/fixtures_test.go` (black-box `package fixtures_test`):

```go
func TestBuildDispatchByHost(t *testing.T) {
	cases := []struct {
		host    string
		wantSub string // a substring unique to that vendor's error envelope
	}{
		{"api.openai.com", `"rate_limit_error"`},
		{"api.anthropic.com", `"type":"error"`},
		{"unknown.example.com", `"api_error"`},
	}
	for _, c := range cases {
		syn := fixtures.Build(c.host, faulttypes.Fault{HTTPStatus: 429})
		if !bytes.Contains(syn.Body, []byte(c.wantSub)) {
			t.Errorf("Build(%q) body = %s, want substring %q", c.host, syn.Body, c.wantSub)
		}
	}
}
```

- [ ] **Step 2: Run it — it should PASS on current code (this is the regression guard, not a red test)**

Run: `go test ./internal/inject/proxy/fixtures/ -run TestBuildDispatchByHost -v`
Expected: PASS. (If it fails, the refactor premise is wrong — stop and re-read `fixtures.go`.)

- [ ] **Step 3: Refactor `Build` from `switch host` to an ordered table**

In `internal/inject/proxy/fixtures/fixtures.go`, replace the `Build` switch with:

```go
// vendorTemplate matches a host to the error-body synthesizer for that
// vendor. Ordered; first match wins. A provider is added by appending a row.
type vendorTemplate struct {
	match func(host string) bool
	body  func(status int) []byte
}

var vendorTemplates = []vendorTemplate{
	{func(h string) bool { return h == "api.openai.com" }, openAIErrorBody},
	{func(h string) bool { return h == "api.anthropic.com" }, anthropicErrorBody},
}

// Build returns a Synthetic for the given host and fault. If
// fault.ResponseBody is set, it is returned verbatim; otherwise the body is
// synthesized from the first matching vendor template, or a generic shape.
func Build(host string, fault faulttypes.Fault) Synthetic {
	for _, t := range vendorTemplates {
		if t.match(host) {
			return vendorResponse(fault, t.body)
		}
	}
	return vendorResponse(fault, genericErrorBody)
}
```

- [ ] **Step 4: Run the fixtures package tests — all green (behavior preserved)**

Run: `go test ./internal/inject/proxy/fixtures/...`
Expected: PASS (the new test + all pre-existing fixture tests).

- [ ] **Step 5: Skip empty-`baseURLEnv` providers in base-URL derivation, and allow a glob `upstream`**

In `internal/inject/proxy/providers.go`, update the `providersForHostGlobs` loop so a forward-proxy-only provider (no base-URL env) is never selected for base-URL injection:

```go
func providersForHostGlobs(globs []string) []provider {
	var out []provider
	for _, p := range providerRegistry {
		if len(p.baseURLEnv) == 0 {
			continue // forward-proxy-only provider; not injectable as a base URL
		}
		for _, g := range globs {
			if g != "" && globMatch(g, p.upstream) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
```

Update the `upstream` field doc comment on the `provider` struct to add one line:

```go
	//   - upstream:   the real API host faultkit fronts and forwards to. For a
	//     forward-proxy-only provider it may be a host glob
	//     (e.g. bedrock-runtime.*.amazonaws.com) used only for match/attribution.
```

- [ ] **Step 6: Add a providers test that empty-baseURLEnv providers are excluded from base-URL selection**

Add to `internal/inject/proxy/providers_test.go` (match its existing package clause):

```go
func TestProvidersForHostGlobsSkipsForwardProxyOnly(t *testing.T) {
	// Every provider that lacks base-URL env must never appear in base-URL
	// derivation, even if its host glob matches.
	for _, p := range providerRegistry {
		if len(p.baseURLEnv) != 0 {
			continue
		}
		got := providersForHostGlobs([]string{p.upstream})
		for _, sel := range got {
			if sel.id == p.id {
				t.Errorf("provider %q has no base-URL env but was selected for base-URL", p.id)
			}
		}
	}
}
```

(On current registry this passes vacuously — openai/anthropic both have base-URL env. It becomes load-bearing once WP-B adds bedrock. Keep it: it pins the invariant.)

- [ ] **Step 7: Run the full proxy package tests + lint**

Run: `go test ./internal/inject/proxy/... && make lint`
Expected: PASS.

- [ ] **Step 8: Run the security gate**

Run: `make sec`
Expected: gosec + nilaway zero findings.

- [ ] **Step 9: Commit**

```bash
git add internal/inject/proxy/fixtures/fixtures.go internal/inject/proxy/fixtures/fixtures_test.go internal/inject/proxy/providers.go internal/inject/proxy/providers_test.go
git commit -m "refactor: table-driven vendor dispatch; guard base-URL provider selection

Prepares the proxy for a forward-proxy-only provider (Bedrock): Build's
host switch becomes an appendable table, and base-URL derivation skips
providers without base-URL env so a glob-hosted provider can't leak into
origin mode. No behavior change for openai/anthropic.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

Open the PR for `refactor/fixture-host-registry`.

---

## WP-B · `feat/bedrock-provider-core` (provider + rate-limited)

Add Bedrock as a registered provider with its AWS-JSON error envelope and a `rate-limited` fixture, so the existing `llm-api-degraded` scenario auto-fires against Bedrock. **Do not** touch anthropic-only modes (overloaded/refusal/request-too-large/stream-error/tool-use-cutoff) — adding a bedrock fixture to those would leak Bedrock into the `anthropic-*` scenarios.

**Files:**
- Create: `internal/inject/proxy/fixtures/bedrock.go`
- Create: `internal/inject/proxy/fixtures/bedrock_test.go`
- Modify: `internal/inject/proxy/fixtures/fixtures.go` (append bedrock template row)
- Modify: `internal/inject/proxy/fixtures/catalog.go` (bedrock `rate-limited` fixture)
- Modify: `internal/inject/proxy/providers.go` (bedrock registry entry)
- Modify: `internal/scenario/builtin/llm_api_degraded.yaml` (description)
- Modify: `docs/scenarios.md` (Bedrock note)

**Interfaces:**
- Consumes: `fixtures.Build` table (WP-A), `vendorResponse`, `providerRegistry`.
- Produces: `bedrockErrorBody(status int) []byte` in package `fixtures`; a `provider{id:"bedrock"}` registry entry with host glob `bedrock-runtime.*.amazonaws.com` and empty `baseURLEnv`.

- [ ] **Step 1: Write the failing bedrock envelope test**

Create `internal/inject/proxy/fixtures/bedrock_test.go` (`package fixtures_test`):

```go
package fixtures_test

import (
	"bytes"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
)

func TestBedrockErrorEnvelope(t *testing.T) {
	syn := fixtures.Build("bedrock-runtime.us-east-1.amazonaws.com",
		faulttypes.Fault{HTTPStatus: 429})
	if syn.Status != 429 {
		t.Fatalf("status = %d, want 429", syn.Status)
	}
	// AWS JSON error: body is {"message": "..."} — no OpenAI/Anthropic keys.
	if !bytes.Contains(syn.Body, []byte(`"message"`)) {
		t.Errorf("body = %s, want a \"message\" field", syn.Body)
	}
	if bytes.Contains(syn.Body, []byte(`"rate_limit_error"`)) {
		t.Errorf("body = %s, must not use the OpenAI/Anthropic envelope", syn.Body)
	}
}
```

- [ ] **Step 2: Run it — FAIL (bedrock host falls through to generic today)**

Run: `go test ./internal/inject/proxy/fixtures/ -run TestBedrockErrorEnvelope -v`
Expected: FAIL — generic body is `{"error":{"message":...,"type":"api_error"}}`, so the `"rate_limit_error"` assertion may pass but the shape is wrong; primarily it proves bedrock isn't dispatched yet.

- [ ] **Step 3: Create the bedrock error-body synthesizer**

Create `internal/inject/proxy/fixtures/bedrock.go`:

```go
package fixtures

import (
	"encoding/json"
	"net/http"
)

// Bedrock (AWS JSON protocol) error bodies are {"message": "..."}; the
// exception type travels in the x-amzn-errortype response header, set by the
// catalog fixture, not here.
func bedrockErrorBody(status int) []byte {
	if b, ok := bedrockErrorBodies[status]; ok {
		return b
	}
	return marshalBedrockError(http.StatusText(status))
}

var bedrockErrorBodies = map[int][]byte{
	http.StatusTooManyRequests:    marshalBedrockError("Too many requests, please wait before trying again."),
	http.StatusServiceUnavailable: marshalBedrockError("The service is temporarily unable to process your request. Please try again later."),
	http.StatusRequestTimeout:     marshalBedrockError("The model did not respond within the allotted time."),
	http.StatusBadRequest:         marshalBedrockError("The provided input is invalid or exceeds the maximum allowed size."),
}

func marshalBedrockError(msg string) []byte {
	out, _ := json.Marshal(map[string]string{"message": msg})
	return out
}
```

- [ ] **Step 4: Append the bedrock template row to `Build`**

In `internal/inject/proxy/fixtures/fixtures.go`, add a row to `vendorTemplates`:

```go
var vendorTemplates = []vendorTemplate{
	{func(h string) bool { return h == "api.openai.com" }, openAIErrorBody},
	{func(h string) bool { return h == "api.anthropic.com" }, anthropicErrorBody},
	{isBedrockHost, bedrockErrorBody},
}

// isBedrockHost matches bedrock-runtime.<region>.amazonaws.com (and the -fips
// variant) without needing a glob engine in this package.
func isBedrockHost(h string) bool {
	return strings.HasPrefix(h, "bedrock-runtime.") && strings.HasSuffix(h, ".amazonaws.com")
}
```

Add `"strings"` to the imports.

- [ ] **Step 5: Run the envelope test — PASS**

Run: `go test ./internal/inject/proxy/fixtures/ -run TestBedrockErrorEnvelope -v`
Expected: PASS.

- [ ] **Step 6: Register the bedrock provider**

In `internal/inject/proxy/providers.go`, append to `providerRegistry`:

```go
	{
		id:       "bedrock",
		upstream: "bedrock-runtime.*.amazonaws.com", // host glob: match/attribution only
		// baseURLEnv intentionally empty — Bedrock is forward-proxy only
		// (SigV4 breaks base-URL origin rewrite). See BEDROCK_PROVIDER_DESIGN.md.
	},
```

- [ ] **Step 7: Add the bedrock `rate-limited` fixture with the errortype header**

In `internal/inject/proxy/fixtures/catalog.go`, extend the `rate-limited` mode:

```go
	"rate-limited": {
		"openai":    {Path: "/v1/*", Status: 429, Headers: map[string]string{"Retry-After": "30"}},
		"anthropic": {Path: "/v1/*", Status: 429, Headers: map[string]string{"Retry-After": "30"}},
		"bedrock":   {Path: "/model/*", Status: 429, Headers: map[string]string{"x-amzn-errortype": "ThrottlingException"}},
	},
```

- [ ] **Step 8: Write a test that `llm-api-degraded` now fans out to bedrock**

Add to `internal/inject/proxy/expand_builtin_test.go` (or a new `bedrock_expand_test.go` matching its package):

```go
func TestLLMAPIDegradedFansOutToBedrock(t *testing.T) {
	s, err := scenario.LoadBuiltin("llm-api-degraded")
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := expandScenario(s, "")
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, e := range expanded.Experiments {
		hosts = append(hosts, e.Match.Host)
	}
	want := "bedrock-runtime.*.amazonaws.com"
	found := false
	for _, h := range hosts {
		if h == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expanded hosts = %v, want one to be %q", hosts, want)
	}
}
```

- [ ] **Step 9: Run it — PASS; run the full proxy suite**

Run: `go test ./internal/inject/proxy/...`
Expected: PASS.

- [ ] **Step 10: Verify a Bedrock request matches and synthesizes end-to-end (matcher)**

Add a matcher test asserting a regional Bedrock host hits the expanded experiment:

```go
func TestMatcherFiresOnRegionalBedrockHost(t *testing.T) {
	s, _ := scenario.LoadBuiltin("llm-api-degraded")
	expanded, _ := expandScenario(s, "bedrock")
	m := NewMatcher(expanded)
	exp := m.matchHostPath("bedrock-runtime.eu-west-1.amazonaws.com", "/model/anthropic.claude-3/invoke")
	if exp == nil {
		t.Fatal("expected a match for a regional Bedrock invoke path")
	}
}
```

- [ ] **Step 11: Update the scenario description + docs**

`internal/scenario/builtin/llm_api_degraded.yaml`: change description to
`Inject 429/503/timeout into requests to OpenAI, Anthropic, and Bedrock.`

`docs/scenarios.md`: under [Failure modes and providers], note that `bedrock` is a third provider (forward-proxy only; `--provider bedrock`); add `bedrock` to the cross-provider table note.

- [ ] **Step 12: Full gates**

Run: `make lint test sec`
Expected: all green, zero sec findings.

- [ ] **Step 13: Commit**

```bash
git add internal/inject/proxy/fixtures/bedrock.go internal/inject/proxy/fixtures/bedrock_test.go \
        internal/inject/proxy/fixtures/fixtures.go internal/inject/proxy/fixtures/catalog.go \
        internal/inject/proxy/providers.go internal/inject/proxy/expand_builtin_test.go \
        internal/inject/proxy/matcher_test.go internal/scenario/builtin/llm_api_degraded.yaml docs/scenarios.md
git commit -m "feat: add Amazon Bedrock as a forward-proxy provider

Bedrock joins openai/anthropic in the provider registry with its AWS-JSON
error envelope ({\"message\":...} + x-amzn-errortype header). The shared
rate-limited mode gains a bedrock fixture, so llm-api-degraded now fires
ThrottlingException (429) against Bedrock automatically. Forward-proxy only:
SigV4 keeps the client's signature valid under MITM; base-URL mode excludes
Bedrock. Anthropic-only modes are untouched.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

Open the PR for `feat/bedrock-provider-core`.

---

## WP-C · `feat/bedrock-scenarios-body` (Converse bodies + Bedrock-only scenarios)

Add the Converse-shape 200-body fixtures (auto-attach to the cross-provider scenarios) and two Bedrock-only scenarios modeled on the `anthropic-*` family.

**Files:**
- Modify: `internal/inject/proxy/fixtures/bedrock.go` (Converse body constants)
- Modify: `internal/inject/proxy/fixtures/catalog.go` (bedrock fixtures for `malformed-json`, `max-tokens-truncation`, `malformed-tool-use`; new bedrock-only modes `model-timeout`, `service-unavailable`)
- Modify: `internal/inject/proxy/fixtures/bedrock_test.go` (body assertions)
- Create: `internal/scenario/builtin/bedrock_model_timeout.yaml`, `internal/scenario/builtin/bedrock_service_unavailable.yaml`
- Modify: `docs/scenarios.md` (Bedrock scenario rows)

**Interfaces:**
- Consumes: WP-B's `bedrockErrorBody`, provider registry, catalog.
- Produces: catalog entries for modes `malformed-json[bedrock]`, `max-tokens-truncation[bedrock]`, `malformed-tool-use[bedrock]`, `model-timeout[bedrock]`, `service-unavailable[bedrock]`.

- [ ] **Step 1: Add Converse-shape body constants**

Append to `internal/inject/proxy/fixtures/bedrock.go`:

```go
// Bedrock Converse API 200 bodies. Shape:
// {"output":{"message":{"role":"assistant","content":[...]}},"stopReason":...,"usage":...}
const (
	// malformed-json: assistant text is itself syntactically invalid JSON.
	bedrockConverseMalformedJSON = `{"output":{"message":{"role":"assistant","content":[{"text":"{\"action\":\"lookup\",\"args\":{\"id\":42},}"}]}},"stopReason":"end_turn","usage":{"inputTokens":10,"outputTokens":8}}`
	// max-tokens-truncation: valid but truncated, flagged only by stopReason.
	bedrockConverseTruncated = `{"output":{"message":{"role":"assistant","content":[{"text":"The answer is"}]}},"stopReason":"max_tokens","usage":{"inputTokens":10,"outputTokens":16}}`
	// malformed-tool-use: toolUse.input violates the tool schema (id typed as string).
	bedrockConverseMalformedToolUse = `{"output":{"message":{"role":"assistant","content":[{"toolUse":{"toolUseId":"tool_1","name":"lookup_user","input":{"id":"not-an-integer"}}}]}},"stopReason":"tool_use","usage":{"inputTokens":10,"outputTokens":8}}`
)
```

- [ ] **Step 2: Add the cross-provider bedrock body fixtures to the catalog**

In `catalog.go`, extend the three shared body modes:

```go
	"malformed-json": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIMalformedJSON},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicMalformedJSON},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseMalformedJSON},
	},
	"max-tokens-truncation": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAITruncated},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicTruncated},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseTruncated},
	},
	"malformed-tool-use": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIMalformedToolUse},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicMalformedToolUse},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseMalformedToolUse},
	},
```

- [ ] **Step 3: Add the two Bedrock-only modes (single-provider fixtures)**

In `catalog.go`, add:

```go
	// Bedrock-distinctive modes (no cross-provider equivalent). Single-provider
	// fixtures → they only fire via the bedrock-* scenarios, never leak into
	// llm-api-degraded / anthropic-* scenarios.
	"model-timeout": {
		"bedrock": {Path: "/model/*", Status: 408, Headers: map[string]string{"x-amzn-errortype": "ModelTimeoutException"}},
	},
	"service-unavailable": {
		"bedrock": {Path: "/model/*", Status: 503, Headers: map[string]string{"x-amzn-errortype": "ServiceUnavailableException"}},
	},
```

- [ ] **Step 4: Body-shape tests**

Add to `bedrock_test.go`:

```go
func TestBedrockConverseFixtures(t *testing.T) {
	cases := []struct{ mode, wantSub string }{
		{"max-tokens-truncation", `"stopReason":"max_tokens"`},
		{"malformed-tool-use", `"toolUse"`},
		{"malformed-json", `\"args\":{\"id\":42},}`},
	}
	for _, c := range cases {
		f, ok := fixtures.For(c.mode, "bedrock")
		if !ok {
			t.Fatalf("no bedrock fixture for %q", c.mode)
		}
		if !strings.Contains(f.Body, c.wantSub) {
			t.Errorf("%s bedrock body = %s, want substring %q", c.mode, f.Body, c.wantSub)
		}
		if f.Path != "/model/*/converse" {
			t.Errorf("%s bedrock path = %q, want /model/*/converse", c.mode, f.Path)
		}
	}
}

func TestBedrockOnlyModesAreSingleProvider(t *testing.T) {
	for _, mode := range []string{"model-timeout", "service-unavailable"} {
		if _, ok := fixtures.For(mode, "openai"); ok {
			t.Errorf("%q must not have an openai fixture", mode)
		}
		if _, ok := fixtures.For(mode, "anthropic"); ok {
			t.Errorf("%q must not have an anthropic fixture", mode)
		}
		if _, ok := fixtures.For(mode, "bedrock"); !ok {
			t.Errorf("%q must have a bedrock fixture", mode)
		}
	}
}
```

(`For` returns `fixtures.Fixture` whose exported fields include `Body`, `Path`, `Status`, `Headers` — confirm against `catalog.go`.)

- [ ] **Step 5: Add the two Bedrock-only scenario YAMLs**

Create `internal/scenario/builtin/bedrock_model_timeout.yaml`:

```yaml
name: bedrock-model-timeout
description: Bedrock returns HTTP 408 ModelTimeoutException — the model did not respond in time.
experiments:
  - name: model-timeout
    failure: model-timeout
    probability: 0.2
```

Create `internal/scenario/builtin/bedrock_service_unavailable.yaml`:

```yaml
name: bedrock-service-unavailable
description: Bedrock returns HTTP 503 ServiceUnavailableException under load.
experiments:
  - name: service-unavailable
    failure: service-unavailable
    probability: 0.2
```

- [ ] **Step 6: Verify the new scenarios load and list**

Run: `go test ./internal/scenario/... ./internal/inject/proxy/... && go run ./cmd/faultkit scenario list`
Expected: `bedrock-model-timeout` and `bedrock-service-unavailable` appear; all tests green. (The builtin `init()` embeds every `*.yaml`, so a malformed file panics at load — a green `scenario list` is the proof.)

- [ ] **Step 7: Docs + gates**

`docs/scenarios.md`: add a "Bedrock-specific" subsection (mirroring "Anthropic-specific") for `bedrock-model-timeout` and `bedrock-service-unavailable`, and note that the cross-provider 200-body modes now include a Bedrock (Converse) fixture.

Run: `make lint test sec`
Expected: all green.

- [ ] **Step 8: Commit**

```bash
git add internal/inject/proxy/fixtures/bedrock.go internal/inject/proxy/fixtures/catalog.go \
        internal/inject/proxy/fixtures/bedrock_test.go internal/scenario/builtin/bedrock_model_timeout.yaml \
        internal/scenario/builtin/bedrock_service_unavailable.yaml docs/scenarios.md
git commit -m "feat: Bedrock Converse body fixtures + bedrock-only scenarios

Converse-shape 200 bodies attach bedrock to the cross-provider malformed-json,
max-tokens-truncation, and malformed-tool-use modes. Two Bedrock-only modes
(model-timeout 408, service-unavailable 503) ship as their own scenarios like
the anthropic-* family, kept single-provider so they don't leak into shared
scenarios.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

Open the PR for `feat/bedrock-scenarios-body`.

---

## WP-D · `feat/bedrock-eventstream` (DEFERRED — streaming)

**Deferred and not bite-sized here on purpose:** the AWS event-stream format is binary and the correct fixtures are *captured bytes*, not hand-written strings. Writing step-by-step code before capturing a real `converse-stream` frame would be fabrication. Open this WP only after capturing sample frames into `internal/inject/proxy/fixtures/testdata/`.

**Approach when picked up:**
- Capture a real Bedrock `converse-stream` response body (binary) into a testdata file.
- Add a minimal frame walker: AWS event-stream messages are `[4-byte total length][4-byte headers length][4-byte prelude CRC][headers][payload][4-byte message CRC]`, all big-endian. To *count and cut* you only need the total-length prefix to hop message to message — no CRC validation.
- Dispatch on `Content-Type: application/vnd.amazon.eventstream` in the response handler, alongside the existing SSE path in `streaming.go` (`isSSE`). Forward N whole frames then close for `streaming-cutoff`; for a Bedrock `stream-error` mode, emit a synthetic `ModelStreamErrorException` event frame mid-stream.
- Add catalog entries: `streaming-cutoff[bedrock]` (Path `/model/*/converse-stream`, `StreamCutoffTokens: N`) and a bedrock-only `stream-error` mode + `bedrock-stream-error.yaml`.

**Verify (when built):** a frame-boundary unit test over the captured bytes cuts after exactly N frames; a Python `boto3` `converse-stream` integration test sees a truncated stream.

---

## WP-E · `fix/stream-cutoff-token-fidelity` (independent, easy)

`streaming.go` counts every `data:` line as a "token", but a `data:` line is an SSE *event* — `message_start`, `content_block_start`, `ping` all carry `data:`. So `stream_cutoff_tokens: 80` cuts after 80 events, not 80 content tokens. Count only content-bearing deltas. Do **not** rename the locked `stream_cutoff_tokens` field; fix the counting + the docs.

**Files:**
- Modify: `internal/inject/proxy/streaming.go`
- Test: `internal/inject/proxy/streaming_test.go`
- Modify: `docs/yaml-schema.md`, `docs/scenarios.md` (clarify the field means "content deltas")

**Interfaces:**
- Produces: `wrapStreamCutoff` unchanged signature; internal counting now gated by `isContentDelta(line []byte) bool`.

- [ ] **Step 1: Write a failing test — a leading non-content event must not count**

Add to `internal/inject/proxy/streaming_test.go`:

```go
func TestStreamCutoffCountsContentDeltasOnly(t *testing.T) {
	// One message_start (data:, not content) then 3 content deltas.
	// Cut at 2 must forward through the 2nd content delta, not stop at message_start.
	upstream := "event: message_start\n" +
		`data: {"type":"message_start"}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"text":"a"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"text":"b"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"text":"c"}}` + "\n\n"

	res := sseResponse(upstream) // helper: *http.Response, Content-Type text/event-stream
	wrapStreamCutoff(res, 2, nil)
	got, _ := io.ReadAll(res.Body)

	// message_start passes through; content deltas a and b pass; c is cut.
	if !bytes.Contains(got, []byte(`"text":"b"`)) {
		t.Errorf("cut too early: %q", got)
	}
	if bytes.Contains(got, []byte(`"text":"c"`)) {
		t.Errorf("cut too late — 3rd content delta leaked: %q", got)
	}
}
```

(Reuse or add the `sseResponse` helper matching the existing streaming test's style.)

- [ ] **Step 2: Run — FAIL (today message_start counts, so cut at 2 stops after the 1st content delta)**

Run: `go test ./internal/inject/proxy/ -run TestStreamCutoffCountsContentDeltasOnly -v`
Expected: FAIL.

- [ ] **Step 3: Count content deltas only**

In `streaming.go`, replace the `bytes.HasPrefix(line, []byte("data:"))` gate:

```go
			if isContentDelta(line) {
				eventCount++
				if eventCount >= cutAt {
					return
				}
			}
```

Add:

```go
// isContentDelta reports whether an SSE line carries model content (a token-ish
// delta), as opposed to lifecycle events (message_start, ping, content_block_start).
// ponytail: substring heuristic over the two provider shapes we ship — OpenAI
// (choices[].delta.content) and Anthropic (content_block_delta). Refine to a
// JSON parse only if a provider's shape starts to alias.
func isContentDelta(line []byte) bool {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return false
	}
	return bytes.Contains(line, []byte("content_block_delta")) || // Anthropic
		(bytes.Contains(line, []byte(`"delta"`)) && bytes.Contains(line, []byte(`"content"`))) // OpenAI
}
```

- [ ] **Step 4: Run — PASS; run the streaming suite**

Run: `go test ./internal/inject/proxy/ -run TestStream -v`
Expected: PASS. (If an existing streaming test asserted the old event-counting semantics, update it to content-delta semantics and note the change in the commit.)

- [ ] **Step 5: Docs**

`docs/yaml-schema.md` + `docs/scenarios.md`: change the `stream_cutoff_tokens` description to "cuts the stream after N content deltas (≈ tokens); lifecycle events like `message_start` don't count."

- [ ] **Step 6: Gates + commit**

```bash
make lint test sec
git add internal/inject/proxy/streaming.go internal/inject/proxy/streaming_test.go docs/yaml-schema.md docs/scenarios.md
git commit -m "fix: stream-cutoff counts content deltas, not SSE lifecycle events

stream_cutoff_tokens previously counted every data: line, so a leading
message_start/ping consumed the budget and the cut landed early. Count only
content-bearing deltas (OpenAI delta.content, Anthropic content_block_delta).
Field name is a frozen schema contract and is unchanged; docs clarified.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

Open the PR for `fix/stream-cutoff-token-fidelity`.

---

## WP-F · `feat/check-provider-list` (independent, easy)

`faultkit check` reports available *modes* but not *providers*. List the registered providers and, per provider, the failure modes with a fixture — so a user can see Bedrock was added and what fires against it. Uses only existing exports (`proxy.ProviderIDs`, `fixtures.Modes`, `fixtures.For`).

**Files:**
- Modify: `internal/cli/check.go`
- Test: `internal/cli/check_test.go` (create if absent; else add a case)

**Interfaces:**
- Consumes: `proxy.ProviderIDs() []string`, `fixtures.Modes() []string`, `fixtures.For(mode, provider) (Fixture, bool)`.
- Produces: extra lines on `runCheck`'s output; exit-code behavior unchanged.

- [ ] **Step 1: Write the failing test**

Create/extend `internal/cli/check_test.go` (`package cli`, since `runCheck` is unexported — match the file's existing package):

```go
func TestRunCheckListsProviders(t *testing.T) {
	var buf bytes.Buffer
	if err := runCheck(&buf); err != nil {
		// runCheck errors only when no modes are available; proxy is always
		// available, so this should not happen on any host.
		t.Fatalf("runCheck: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"providers:", "openai", "anthropic", "bedrock"} {
		if !strings.Contains(out, want) {
			t.Errorf("check output missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run — FAIL (no provider section yet)**

Run: `go test ./internal/cli/ -run TestRunCheckListsProviders -v`
Expected: FAIL.

- [ ] **Step 3: Print the provider/mode list in `runCheck`**

In `internal/cli/check.go`, after the modes `tw.Flush()` and before the `anyAvailable` check, add:

```go
	fmt.Fprintln(out, "\nproviders:")
	pw := tabwriter.NewWriter(out, 0, 0, 1, ' ', 0)
	for _, id := range proxy.ProviderIDs() {
		var modes []string
		for _, m := range fixtures.Modes() {
			if _, ok := fixtures.For(m, id); ok {
				modes = append(modes, m)
			}
		}
		fmt.Fprintf(pw, "  %s\t%s\n", id, strings.Join(modes, ", "))
	}
	if err := pw.Flush(); err != nil {
		return err
	}
```

Add imports: `strings`, `github.com/faultkit/faultkit/internal/inject/proxy`, `github.com/faultkit/faultkit/internal/inject/proxy/fixtures`.

- [ ] **Step 4: Run — PASS**

Run: `go test ./internal/cli/ -run TestRunCheckListsProviders -v`
Expected: PASS.

- [ ] **Step 5: Eyeball the real output**

Run: `go run ./cmd/faultkit check`
Expected: the existing modes block, then a `providers:` block listing openai/anthropic/bedrock with their modes.

- [ ] **Step 6: Gates + commit**

```bash
make lint test sec
git add internal/cli/check.go internal/cli/check_test.go
git commit -m "feat: faultkit check lists providers and their failure modes

check reported modes but not providers; users had no way to see that Bedrock
was added or which failure modes fire against each provider. Adds a providers
block built from the existing registry + fixture catalog. Exit codes unchanged.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

Open the PR for `feat/check-provider-list`.

---

## Self-review

**Spec coverage (against `BEDROCK_PROVIDER_DESIGN.md`):**
- Forward-proxy-only + SigV4 pass-through → WP-B (registry entry, empty baseURLEnv; the mechanism is the *existing* MITM path, so there is no new forwarding code — pass-through is inherited unchanged and is asserted via the "regional host matches" test). ✅
- Base-URL exclusion → WP-A Step 5–6 (skip empty-baseURLEnv). ✅
- Region host / endpoint paths → WP-B (`isBedrockHost`, `upstream` glob, `/model/*` paths). ✅
- AWS-JSON error envelope + x-amzn-errortype → WP-B (`bedrockErrorBody`, fixture Headers). ✅
- Converse body modes → WP-C. ✅
- Bedrock-only modes as own scenarios → WP-C (model-timeout, service-unavailable). ✅
- Streaming (event-stream) → WP-D, explicitly deferred with rationale. ✅
- Easy gaps T5 + provider listing → WP-E, WP-F. ✅
- HTTP-status-per-exception confirmed against live Bedrock → the design note's validation gate; WP-B/WP-C tests assert *shape*, and the PR description records the live-checked status. ⚠️ **Open item:** 408 for `ModelTimeoutException` and 400/413 for oversized input are the expected values to verify against a live response during WP-C; if AWS returns a different status, update the fixture and test together.

**Placeholder scan:** none — every code step shows real code. WP-D is intentionally an outline, not placeholders, and says so.

**Type consistency:** `fixtures.Fixture{Path,Status,Headers,Body,StreamCutoffTokens}`, `fixtures.For/Modes/Build`, `provider{id,upstream,baseURLEnv,pathPrefix,apiBase}`, `providersForHostGlobs`, `expandScenario`, `NewMatcher/matchHostPath`, `wrapStreamCutoff`, `runCheck`, `proxy.ProviderIDs` — all match the files read during planning. `isBedrockHost`/`isContentDelta`/`vendorTemplate` are new and defined where first used.

**Scope:** one provider + two independent easy fixes; WP-D deferred. Single-provider focus, no subsystem sprawl.
