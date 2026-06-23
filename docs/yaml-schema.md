# Scenario YAML schema

The schema for `--config <file>.yaml` and the embedded builtin
scenarios. The v0.1 schema is stable; the `failure` and `provider`
experiment fields were added **additively** — every existing scenario
keeps loading unchanged.

---

## Top level

```yaml
name: my-scenario              # required
description: |                  # optional
  Free-form description shown by `faultkit scenario list`.
requires:                       # optional
  platform: linux               # optional, currently only "linux" is meaningful
  kernel_min: "5.8"             # optional, advisory; faultkit check uses it
experiments:                    # required, at least one
  - ...
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Kebab-case (`[a-z0-9-]+`, must start with a letter). Identifies the scenario in `faultkit scenario list`. |
| `description` | string | no | One-line summary shown by `scenario list`. |
| `requires.platform` | string | no | Currently only `linux` is meaningful (eBPF scenarios). Absence = no constraint. |
| `requires.kernel_min` | string | no | Advisory minimum kernel (e.g. `"5.8"` for ringbuf). `faultkit check` reports it. |
| `experiments` | list | yes | At least one. See below. |

## Experiment

Each experiment is one fault rule, written in one of two mutually
exclusive forms. Within a scenario they're evaluated in YAML order —
first match wins.

- **Raw** — an explicit `fault` + `match` (full control over any
  host/path/syscall). Use for custom scenarios.
- **Fixture-driven** — a named `failure` mode, optionally narrowed to a
  `provider`, resolved against faultkit's per-provider fixture catalog.
  Used by the builtin LLM scenarios.

### Raw form

```yaml
- name: openai-rate-limited     # required
  fault:                         # required (raw form)
    http_status: 429
    response_headers:
      Retry-After: "30"
  match:                         # required (raw form)
    host: api.openai.com
    path: /v1/chat/completions
  probability: 0.2               # required, 0.0..1.0
```

### Fixture-driven form

```yaml
- name: rate-limited            # required
  failure: rate-limited          # required (fixture-driven form): a failure-mode id
  provider: anthropic            # optional: omit to fan out across every provider
  probability: 0.2               # required, 0.0..1.0
```

`failure` names a mode in the fixture catalog. At injection time faultkit
expands it into one concrete experiment per in-scope provider, supplying
that provider's host, path, and response shape — so you don't hand-write
them. With `provider` omitted it fans out across every provider that has a
fixture for the mode; set the field (or pass `--provider` on the CLI) to
narrow to one. Some modes are provider-specific and have only one fixture
(e.g. `overloaded` → Anthropic), so they fan out to just that provider.

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Free-form identifier; appears in events and reports. |
| `failure` | string | fixture-driven | A failure-mode id (e.g. `rate-limited`, `malformed-json`, `streaming-cutoff`). Mutually exclusive with `fault`/`match`. |
| `provider` | string | no | Restrict a fixture-driven experiment to one provider id (`openai`, `anthropic`). Only valid alongside `failure`. |
| `fault` | object | raw | What to inject. See [Fault](#fault). |
| `match` | object | raw | When to inject. See [Match](#match). |
| `probability` | float | yes | 0.0–1.0 inclusive. Independent dice roll per matched request (proxy) or syscall (eBPF). |

## Fault

A `Fault` is either HTTP-level or syscall-level. Mixing fields from
both is a validation error.

### HTTP-level fault (proxy mode)

```yaml
fault:
  http_status: 429                     # any HTTP status code; required if no other field set
  response_headers:                    # optional
    Retry-After: "30"
    X-Some-Header: value
  response_body: '{"error":"..."}'     # optional, replaces upstream body
  stream_cutoff_tokens: 80             # optional, mutually exclusive with response_body
```

| Field | Type | Notes |
|---|---|---|
| `http_status` | int | The status code the proxy returns to the target. Sent with `X-Faultkit-Synthetic: true`. |
| `response_headers` | map[string]string | Merged into the synthetic response. |
| `response_body` | string | If set, replaces the upstream body verbatim. |
| `stream_cutoff_tokens` | int | For SSE streams: forwards N `data:` events from upstream then drops the connection without `[DONE]`. Mutually exclusive with `response_body`. |

A fault is HTTP-level if any of `http_status`, `response_headers`,
`response_body`, or `stream_cutoff_tokens` is set.

### Syscall-level fault (eBPF mode)

```yaml
fault:
  errno: ECONNRESET                    # symbolic name
```

| Field | Type | Notes |
|---|---|---|
| `errno` | string | Symbolic errno name. v0.1 supports `ECONNRESET` (with `match.syscall: recvmsg`) and `EACCES` (with `match.syscall: openat`). |

## Match

Like `fault`, a `Match` is either HTTP-level or syscall-level — never
both.

### HTTP match (proxy mode)

```yaml
match:
  host: api.openai.com         # glob: * matches any chars including /
  path: /v1/*                  # glob: * matches any chars including /
```

| Field | Type | Notes |
|---|---|---|
| `host` | string | Glob against the request's host (no port). `*` matches any run of characters; `?` matches a single character. The request host is lowercased before matching, so write your pattern in lowercase. |
| `path` | string | Glob against the request URL path. Same wildcard semantics as `host`. Optional — absence means "match any path on this host". |

### Syscall match (eBPF mode)

```yaml
match:
  syscall: recvmsg
```

| Field | Type | Notes |
|---|---|---|
| `syscall` | string | Bare syscall name. v0.1 supports `recvmsg` and `openat`. |

## Validation rules

`faultkit run` rejects the scenario at load time if any of these
fail:

- Scenario name not kebab-case.
- No experiments.
- Experiment with empty `name`.
- `probability` outside [0, 1].
- An experiment sets both a `failure` mode and a raw `fault`/`match`.
- `provider` is set without a `failure` mode.
- `fault` mixes HTTP-level and syscall-level fields.
- `match` mixes HTTP-level (`host`/`path`) and syscall-level (`syscall`) fields.
- `match` is empty.
- `fault` and `match` disagree on level (e.g. HTTP fault with syscall match).

Errors come back as exit code `4` (usage error).

A fixture-driven experiment is additionally checked when the injector
starts: an unknown `failure` mode, an unknown `provider`, or a provider
that has no fixture for the mode fails the run with a clear message.

## Examples

A complete builtin: [`internal/scenario/builtin/llm_api_degraded.yaml`](../internal/scenario/builtin/llm_api_degraded.yaml).

User-supplied scenarios in the demo examples: [`examples/<scenario>/scenario.yaml`](../examples/).

## Versioning

The schema is part of the user contract. v0.1 fields stay; v0.2 may
add fields, never remove or repurpose them. If you write a scenario
against v0.1, it'll keep loading on later versions.
