# Scenario YAML schema

The schema for `--config <file>.yaml` and the embedded builtin
scenarios. The v0.1 schema is stable; the `failure` and `provider`
experiment fields, `match.method`, and the `delay_ms` and
`delay_jitter_ms` fault fields were added **additively**, so every existing
scenario keeps loading unchanged.

---

## Top level

```yaml
name: my-scenario              # required
description: |                  # optional
  Free-form description shown by `faultkit scenario list`.
requires:                       # optional
  platform: linux               # optional, advisory; not enforced
  kernel_min: "5.8"             # optional, advisory; not enforced
experiments:                    # required, at least one
  - ...
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Kebab-case (`[a-z0-9-]+`, must start with a letter). Identifies the scenario in `faultkit scenario list`. |
| `description` | string | no | One-line summary shown by `scenario list`. |
| `requires.platform` | string | no | Advisory: currently only `linux` is meaningful (eBPF scenarios). faultkit doesn't enforce it; eBPF mode checks the host itself. |
| `requires.kernel_min` | string | no | Advisory minimum kernel (e.g. `"5.8"` for ringbuf). Not enforced: neither `faultkit check` nor `faultkit run` reads it. eBPF mode itself requires Linux 5.8+. |
| `experiments` | list | yes | At least one. See below. |

## Experiment

Each experiment is one fault rule, written in one of two mutually
exclusive forms. Within a scenario they're evaluated in YAML order —
first match wins. If the matching experiment's dice roll declines, the
request passes through unfaulted; later experiments aren't tried. eBPF
mode only uses a scenario's first syscall experiment.

- **Raw** — an explicit `fault` + `match` (full control over any
  host/path/syscall). Use for custom scenarios.
- **Fixture-driven** — a named `failure` mode, optionally narrowed to a
  `provider`, resolved against faultkit's per-provider fixture catalog.
  Used by the builtin LLM and vector-DB scenarios.

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
| `provider` | string | no | Restrict a fixture-driven experiment to one provider id (`openai`, `anthropic`, `bedrock`, `pinecone`, `qdrant`; `faultkit check` lists them with their modes). Only valid alongside `failure`. |
| `fault` | object | raw | What to inject. See [Fault](#fault). |
| `match` | object | raw | When to inject. See [Match](#match). |
| `probability` | float | yes | 0.0–1.0 inclusive. Independent dice roll per matched request (proxy) or syscall (eBPF). |

## Fault

A `Fault` is either HTTP-level or syscall-level. Mixing fields from
both is a validation error. The [delay](#delay-level-neutral) fields
belong to neither level and combine with either.

### HTTP-level fault (proxy mode)

```yaml
fault:
  http_status: 429                     # any HTTP status code; required if no other field set
  response_headers:                    # optional
    Retry-After: "30"
    X-Some-Header: value
  response_body: '{"error":"..."}'     # optional, replaces upstream body
  stream_cutoff_tokens: 80             # optional; when set, the fields above are ignored
```

| Field | Type | Notes |
|---|---|---|
| `http_status` | int | The status code the proxy returns to the target. Sent with `X-Faultkit-Synthetic: true`. |
| `response_headers` | map[string]string | Merged into the synthetic response. |
| `response_body` | string | If set, replaces the upstream body verbatim. |
| `stream_cutoff_tokens` | int | For SSE streams: forwards N content deltas (≈ tokens) from upstream then drops the connection without `[DONE]`. Lifecycle events like `message_start` don't count. When set, faultkit forwards the real upstream response and ignores `http_status`, `response_headers`, and `response_body`; combining them isn't rejected. |

A fault is HTTP-level if any of `http_status`, `response_headers`,
`response_body`, or `stream_cutoff_tokens` is set.

A `stream_cutoff_tokens` fault counts as fired only when faultkit cuts a
stream: the response is SSE (`Content-Type: text/event-stream`) and reaches
N content deltas. A non-SSE response, or a stream that ends with fewer than
N deltas, passes through unchanged and adds no event. A line longer than
1 MiB also passes through, but doesn't count as a delta. If the upstream
read fails before the cut point, the report records an event with
`fired: false` and the error in `err`. That event doesn't count as fired.
In forward-proxy mode the proxy buffers its writes to the client, so a
client that disconnects mid-stream can still be counted as fired.

### Syscall-level fault (eBPF mode)

```yaml
fault:
  errno: ECONNRESET                    # symbolic name
```

| Field | Type | Notes |
|---|---|---|
| `errno` | string | Symbolic errno name. v0.1 supports `ECONNRESET` (with `match.syscall: recvmsg`) and `EACCES` (with `match.syscall: openat`). |

### Delay (level-neutral)

```yaml
fault:
  delay_ms: 8000                       # hold each matched request 8 s
  delay_jitter_ms: 7000                # optional: plus a random 0–7000 ms
```

| Field | Type | Notes |
|---|---|---|
| `delay_ms` | int | Milliseconds faultkit holds a matched request before anything else happens to it. With no other field set, the request is then forwarded unchanged to the real upstream, so the response (streamed or not) starts that much later. With `http_status`, `response_headers` or `response_body`, the synthetic response is sent after the delay and the upstream is never contacted. With `stream_cutoff_tokens`, the request is forwarded after the delay and the stream is cut as usual. |
| `delay_jitter_ms` | int | Adds a random 0 to `delay_jitter_ms` ms (both ends included), drawn per request. Requires `delay_ms`. |

A delay is level-neutral: it counts as neither an HTTP-level nor a
syscall-level field, so a delay-only fault is valid and its `match`
decides what it holds. A delay with an HTTP match runs in proxy mode. A
delay with a syscall match passes validation, but eBPF mode can't delay a
syscall yet. The run fails at start (exit `2`) when the delay is on the
scenario's first syscall experiment, the only one eBPF mode reads, and the
run uses eBPF mode, which auto mode picks only when the scenario has no
HTTP experiment. Otherwise the delayed syscall experiment is skipped, like
any syscall experiment the chosen mode doesn't read.

Both fields must be non-negative, and `delay_ms + delay_jitter_ms` must
not exceed `300000` (5 minutes).

A delayed request counts as fired once, when its delay starts: a fired
delay means the hold started. It counts even if the client gave up or the
run ended before the delay ran out, and then any status, headers, body or
stream cut meant to follow the delay never reached the client. The report
doesn't say which part of the fault was applied. What follows the delay
doesn't add a second event either, so with `stream_cutoff_tokens` the
delay alone counts as an applied fault, even if the stream never reaches
the cut point.

When the run ends (the target exits), faultkit cuts every pending delay
short and drops those connections; a held request is never forwarded
after that. In base-URL mode, a client that disconnects during the delay
of a request without a body (such as a `GET`) ends the delay the same
way. faultkit doesn't notice a client that gives up during the delay of a
request with a body (a `POST`, as most LLM calls are), or any client in
forward-proxy mode: if the run is still going when the delay runs out,
the request is forwarded upstream anyway. For a delay-only fault, or a
delay with `stream_cutoff_tokens` (also forwarded after its delay), that
is a real, possibly billed, API call.

In forward-proxy mode the proxy gives each request on a client connection
5 minutes, counted from when it starts waiting for that request. The
delay, the upstream call and writing the response all have to fit in
that window. A delay close to the cap leaves no time for the rest: the
client sees the connection close instead of a response, yet a request
that is forwarded after its delay (delay-only, or with
`stream_cutoff_tokens`) can still reach the upstream, in full or with its
body cut short.

## Match

Like `fault`, a `Match` is either HTTP-level or syscall-level — never
both.

### HTTP match (proxy mode)

```yaml
match:
  host: api.openai.com         # glob: * matches any chars including /
  path: /v1/*                  # glob: * matches any chars including /
  method: POST                 # optional: only requests with this method
```

| Field | Type | Notes |
|---|---|---|
| `host` | string | Glob against the request's host (no port). `*` matches any run of characters; `?` matches a single character. The request host is lowercased before matching, so write your pattern in lowercase. Required in practice: a match with only `path` passes validation but never fires. |
| `path` | string | Glob against the request URL path. Same wildcard semantics as `host`. Optional — absence means "match any path on this host". |
| `method` | string | Optional. One of `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, in any case. Only requests with that method match; absence means any method. It narrows a `host`/`path` match and selects nothing on its own, so set it alongside `host`. A request whose method doesn't match moves on to the next experiment, so one experiment per method can share a host and path. |

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
- `match` mixes HTTP-level (`host`/`path`/`method`) and syscall-level (`syscall`) fields.
- `match` is empty. A `method` on its own counts as empty.
- `match.method` is not one of `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`.
- `fault` and `match` disagree on level (e.g. HTTP fault with syscall match). The delay fields have no level, so they never cause this.
- `delay_ms` or `delay_jitter_ms` is negative, `delay_jitter_ms` is set without `delay_ms`, or `delay_ms + delay_jitter_ms` exceeds `300000`.

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
