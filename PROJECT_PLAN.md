# faultkit project plan

> **Working document.** This is the strategic anchor for faultkit.
> Tactical details for the current iteration live in `V0.1_SPEC.md`.
> Operational rules for working in this repo live in `CLAUDE.md`. When
> the three conflict, `CLAUDE.md` wins for repo behavior, this doc wins
> for strategy, and `V0.1_SPEC.md` wins for v0.1 scope.

---

## 1. Positioning

### 1.1 One-liner

**faultkit is a fault injection toolkit for the AI agent era — purpose-built
for the LLM, RAG, and tool-call failures that mocks can't simulate.**

### 1.2 The problem

Modern AI applications depend on fragile external systems: LLM providers,
vector databases, embedding endpoints, search APIs, tool subprocesses,
and internal microservices. SDKs handle basic retries. Most application
code does not survive the failures those retries can't fix:

- A 429 from OpenAI mid-chain, where the retry uses partial reasoning state and produces a confidently wrong answer
- A streaming response that cuts off mid-token and gets treated as final
- A tool subprocess that's `SIGPIPE`'d before completing, leaving the agent's loop in an invalid state
- A vector DB that returns shuffled or stale results, silently corrupting RAG output
- A cascade of failures across LLM + tool + database that no single test exercises

These failures happen in production. They aren't exercised by unit tests.
They aren't exercised by integration tests against mocks (mocks test
the mock). They aren't exercised by chaos engineering tools built for
Kubernetes infrastructure.

faultkit exercises them.

### 1.3 What faultkit is, mechanically

A single Go binary that wraps a target command (`faultkit run -- pytest ...`),
intercepts the target's network and syscall traffic, and injects
deterministic faults that match real production failure modes. Two
injection mechanisms:

- **HTTPS proxy** for LLM, RAG, gateway, and other HTTP/HTTPS scenarios.
  faultkit terminates TLS with a per-run CA, rewrites HTTP responses
  (status codes, bodies, streaming), forwards or short-circuits as the
  scenario dictates. The target sees a real HTTP 429 from a real TLS
  connection because that's what it gets.
- **eBPF** for syscall-level scenarios on Linux. faultkit loads small
  BPF programs that hook tracepoints (`recvmsg`, `openat`, `write`)
  and use `bpf_override_return` to rewrite syscall return values for
  processes inside the target's PID tree. The target sees a real
  `ECONNRESET`, a real `EACCES`, because that's what the kernel returned.

A future third mechanism (`LD_PRELOAD`/`DYLD` shim in C) extends syscall-level
coverage to non-Linux platforms and to scenarios where neither proxy nor
eBPF fits cleanly. Not in v0.1.

### 1.4 What faultkit is not

These are explicitly out of scope:

- **A retry library or SDK wrapper.** SDKs handle the happy path.
  faultkit exercises the unhappy path. It does not replace SDK calls;
  it intercepts them.
- **An observability tool.** Datadog, Sentry, OpenTelemetry, LangSmith,
  Helicone all tell you what broke after it broke. faultkit triggers
  breakage on demand.
- **A generic chaos monkey.** Chaos Mesh, Gremlin, Litmus inject faults
  at infrastructure boundaries (kill pods, disrupt networks, fill
  disks). faultkit injects faults at the SDK/API boundary, where AI
  failures live.
- **A Kubernetes-native tool.** Single binary, single-shot, runs
  locally and in CI without a cluster.
- **A daemon.** Runs in the foreground, wraps a target, exits when the
  target exits. No background mode. No `serve` subcommand.

### 1.5 Why this positioning

Three reasons it survives scrutiny:

1. **The failures are real and undertested.** Every team shipping
   agents is hitting these failures. Mocks don't catch them. Production
   does.
2. **The mechanism is correctly matched to the failure.** LLM 429s
   live in HTTPS, so an HTTPS proxy that returns real 429s is the
   correct tool. Tool subprocess `SIGPIPE` lives at the pipe stdio
   layer, so eBPF is the correct tool. No layer is forced.
3. **The competitive landscape doesn't address this.** The chaos tools
   were built for the previous era's stack (Kubernetes, microservices,
   TCP-level failures). The AI-eval tools (promptfoo, deepeval, ragas)
   evaluate output quality, not resilience. The observability tools
   observe. There's a gap. faultkit fills it.

---

## 2. The first user

The marketing target is a senior engineer or technical co-founder
building an AI feature who's nervous it'll fail weirdly in production.

Specifically:

- 2–15 person AI startup, or AI-feature team inside a larger company
- Shipping agents, copilots, RAG pipelines, or tool-using agents to production
- Has been paged at least once for a failure they couldn't reproduce locally
- Cares about making the 3am page never happen again
- Reads HN, lobste.rs, agent-framework Discords
- Will install a CLI from a curl-pipe-sh if the demo is good
- Will not fill out a "request a demo" form

The first production users come from this audience. Adjacent enterprise
audiences (platform teams, SRE, AppSec) come later, on the strength of
having shipped something the AI-builder audience already trusts.

---

## 3. Strategic context

### 3.1 What this project is for

faultkit is an open source project built on the same foundations as
[kntrl.dev](https://kntrl.dev). The aims are technical and developer-facing:

1. **Ship a credible, technically dense OSS tool** that contributes
   something useful to the AI developer ecosystem and reinforces the
   author's work in eBPF and AI infrastructure.
2. **Exercise the open-core model** in a way that fits a small-team
   builder shipping into the AI developer audience.
3. **Stay open to commercial signal** if the OSS adoption proves the
   wedge is real, without forcing it.

### 3.2 Operational frame

- **Sustainable cadence.** The project is designed to ship steadily
  on a small time budget rather than sprint and burn out.
- **No outside funding.** The economics work without it.
- **Distribution through OSS adoption + technical writing.** Not
  through paid acquisition, not through enterprise sales motions.
- **Apache 2.0 OSS, perpetual.** A future Pro tier, if it exists,
  monetizes teams, not individuals. The OSS core stays free for
  individuals and open-source projects, always.

### 3.3 Defensibility

What's defensible:

- **The scenario library.** The actual failure recipes,
  vendor-accurate fixtures, and the mapping from production failure
  modes to YAML scenarios. This compounds with use and is the
  project's most valuable long-term asset.
- **The integration depth with agent frameworks.** Working LangChain,
  LlamaIndex, CrewAI, DSPy examples that catch bugs in those
  frameworks' demo agents. Each one is both a marketing artifact and
  a technical commitment.
- **Brand: kntrl.dev heritage + author identity.** Real and meaningful
  for the technical audience the project is built for.
- **Cross-mode coverage.** Few tools do proxy + eBPF cleanly in one
  binary. Building both is meaningful work to replicate.

What's not particularly defensible:

- The proxy itself, as a standalone artifact
- Any individual scenario, as a standalone artifact
- The YAML schema
- The "fault injection for AI" idea
- The eBPF programs (standard libbpf patterns, public knowledge)

The strategic conclusion: depth and breadth of scenarios + agent-framework
integration + brand are the moat. Code surface alone is not.

### 3.4 What success looks like

Success is shaped by what we learn, not by a single number:

- **Adoption signal.** Are agent builders finding faultkit, trying it,
  starring the repo? Is the launch piece resonating?
- **Engagement signal.** Are users opening issues, asking questions,
  filing PRs? Is there a feedback loop forming?
- **Production signal.** Is anyone running faultkit in CI for a real
  codebase? Even a few production users validate the wedge.
- **Commercial signal.** Are teams asking about a paid tier? Not the
  primary aim, but it's the data that resolves whether Pro deserves
  investment.

The plan is built so that the project is recoverable across a wide
range of outcomes. v0.1 is shippable, the time investment is bounded,
and the technical artifact has value as part of the author's body of
work even if the broader adoption signals come in muted.

---

## 4. Architecture

### 4.1 The model

faultkit is structurally three things stacked:

1. **A scenario engine** — loads YAML, validates, dispatches experiments.
2. **An injection layer** — currently proxy + eBPF, future shim. Each
   knows how to apply faults at one layer of the stack.
3. **A runner** — wraps a target process, sets the env, captures exit code.

The CLI is a thin orchestrator over those three.

```text
faultkit run --scenario X -- target-cmd
       │
       ▼
   ┌─────────────────────────┐
   │ scenario engine         │   loads X, validates, gives experiments
   └────────────┬────────────┘
                │
   ┌────────────▼────────────┐
   │ injection layer         │   proxy.Start() returns env vars
   │ (proxy or eBPF)         │   ebpf.Start() loads BPF programs
   └────────────┬────────────┘
                │
   ┌────────────▼────────────┐
   │ runner                  │   forks target with env, waits for exit
   └────────────┬────────────┘
                │
   ┌────────────▼────────────┐
   │ events → report         │   terminal + JSON
   └─────────────────────────┘
                │
                ▼
          exit code
```

### 4.2 The proxy is the hero

For v0.1 and the AI-agent positioning, the proxy carries the marketing,
the demo, and the integration test. Its job:

- Listen on `127.0.0.1:<random>` for HTTP CONNECT
- Generate a per-run ephemeral CA
- Mint leaf certs on demand for any host the target connects to
- Match each request against scenario experiments (host glob + path glob)
- For matched + fault-fired requests: synthesize a vendor-accurate response
- For everything else: pass through to upstream
- Emit fault events for the report

Redirection to the proxy happens via `HTTPS_PROXY` environment variable
in v0.1 (catches the entire Python/Node ecosystem). Cgroup-based eBPF
redirection is deferred to v0.2 for clients that ignore proxy env vars.

### 4.3 eBPF is the supporting mechanism

eBPF in v0.1 ships one scenario (`flaky-network`) as proof that the
multi-mode architecture works. It's not the marketing story. Its job:

- Load a small BPF program that hooks `recvmsg` (and a small set of
  related syscalls)
- Use `bpf_override_return` to inject a configured errno when the
  target's PID matches and the dice roll passes
- All decision logic (probability, matching) lives in Go; the BPF
  program is a dumb policy enforcer reading from an LRU map populated
  by userspace

Future eBPF scenarios (v0.2+): `tool-call-flaky` (subprocess pipe
failures), `tool-permission-denied` (`EACCES` on file access),
`disk-full` (`ENOSPC`), `fd-exhaustion` (`EMFILE`), `slow-dns` (delay
on `getaddrinfo`).

### 4.4 The shim is deferred

`LD_PRELOAD` on Linux, `DYLD_INSERT_LIBRARIES` on macOS. C library
that interposes on libc functions. Two reasons it's deferred to v0.2:

1. v0.1's audience (Python/Node agent code) is fully covered by
   proxy mode for AI scenarios. The shim is for the cases proxy can't
   reach, which mostly involves backend chaos scenarios that aren't
   v0.1's wedge.
2. C interop with Go's process model is fiddly. Better to ship it
   once with real test coverage than rush it for v0.1.

The directory exists with a placeholder README.

### 4.5 The OSS/Pro seam

The OSS repo must contain zero Pro-aware code. The seam is
`pkg/extension`: a small set of generic capability interfaces that
external packages (including a future Pro repo) can implement to add
behavior. The OSS code does not know Pro exists.

If something looks like it should be Pro-aware in OSS, the right
answer is to add a generic capability to `pkg/extension` that Pro
implements. See `CLAUDE.md` "The OSS/Pro boundary" for full rules.

### 4.6 Repository layout

```text
faultkit/
├── cmd/faultkit/             entry point (≤20 LOC of substance)
├── pkg/                      public API — additions require approval
│   ├── extension/            OSS/Pro seam (generic, not Pro-aware)
│   ├── faulttypes/           Fault struct, fault type constants
│   └── scenario/             Scenario, Experiment, YAML loader
├── internal/                 implementation detail — refactor freely
│   ├── cli/                  cobra commands, exit code dispatch
│   ├── inject/
│   │   ├── inject.go         Injector interface, Event type
│   │   ├── proxy/            HTTPS proxy injector
│   │   │   ├── ca.go         per-run CA, leaf minting
│   │   │   ├── server.go     martian-based proxy
│   │   │   ├── matcher.go    host/path matching
│   │   │   ├── faulter.go    fault decision engine
│   │   │   └── fixtures/     vendor-accurate response shapes
│   │   └── ebpf/             eBPF injector (Linux-only)
│   ├── runner/               target process management
│   ├── scenario/builtin/     embedded YAML for shipped scenarios
│   └── report/               terminal + JSON output
├── bpf/                      eBPF C source (GPL-2.0)
├── shim/                     v0.2 placeholder
├── examples/                 working demos
├── test/integration/         integration tests (//go:build integration)
├── docs/                     user-facing docs
├── .github/workflows/        CI + release
├── CLAUDE.md                 operating manual for Claude Code
├── PROJECT_PLAN.md           this file
├── V0.1_SPEC.md              current iteration's tactical spec
├── README.md
├── LICENSE                   Apache 2.0
├── CONTRIBUTING.md
└── CODE_OF_CONDUCT.md
```

`V0.1_SPEC.md` §4 has the exhaustive file-level layout for the current
iteration.

---

## 5. Scope discipline

### 5.1 v0.1: minimum that proves the model

- HTTPS proxy with per-run CA
- One marquee AI scenario: `llm-api-degraded` (OpenAI 429/503/timeout)
- One eBPF scenario: `flaky-network` (TCP recv `ECONNRESET`, Linux 5.8+)
- YAML scenario loading (file + builtin registry)
- `faultkit run` with target wrapping and exit code dispatch
- `faultkit check` reporting available modes honestly
- Terminal + JSON reports
- One worked example: openai-python agent + pytest
- One CI integration: GitHub Actions workflow
- README, quickstart, scenarios reference, launch piece

`V0.1_SPEC.md` is the authoritative scope document. The above is the
summary.

### 5.2 v0.2: AI-scenario depth

The next iteration's scope expands AI coverage and proves the
multi-mode architecture is real:

- Anthropic + Bedrock vendor fixtures
- Streaming/SSE-aware proxy (`llm-streaming-cutoff`)
- `rag-corruption` for Pinecone, Weaviate, Qdrant cloud APIs
- `context-window-squeeze` (proxy-side request body manipulation)
- `tool-call-flaky` (eBPF, subprocess `SIGPIPE` / short reads)
- `tool-permission-denied` (eBPF, `EACCES` on file access)
- The C shim for non-Linux syscall scenarios
- Cgroup-based eBPF redirection for clients that ignore `HTTPS_PROXY`
- LangChain + LlamaIndex worked examples

### 5.3 v0.3 and beyond: positioning bets

If the OSS adoption signal is meaningful, these are the investments
that make the project larger:

- Coverage reporting — "your test suite exercised these scenarios but
  not these others"
- Scenario packs for specific stacks (LangChain, LlamaIndex, AWS
  Bedrock Agents, OpenAI Assistants API)
- Observability correlation (Datadog APM, Sentry, Grafana)
- Exploratory: uprobe-based TLS interception for environments where
  a proxy can't be inserted

### 5.4 Permanently out of scope

Will not be built, period:

- Kubernetes operators, CRDs, Helm charts
- Production fault injection (different blast-radius requirements)
- Per-language SDKs (Python, Node, etc.) — CLI + YAML covers it
- Interactive TUI mode
- Long-running daemon / serve mode
- Per-feature license keys, license validation logic in OSS
- Telemetry, analytics, "phone home" of any kind
- Auto-update logic
- Cloud SaaS dashboard in the OSS repo

If a request leans into any of the above, surface the scope question
before writing code.

---

## 6. The launch

### 6.1 The single highest-leverage artifact

`docs/ai-agent-scenarios.md` — the launch piece. It does not describe
the tool. It tells a story:

1. A real failure mode the reader recognizes from their own production
2. Why mocks and unit tests don't catch it
3. A demonstration of faultkit catching it, with real terminal output
4. The technical narrative — proxy mode for HTTPS, eBPF for syscalls,
   honest about which mechanism does what
5. A worked example against a real agent framework

If this piece lands, the project gets meaningful attention in a week.
Treat it as code: dogfood, iterate, get external review, ship.

### 6.2 Launch week tactics

- Launch day: Tuesday or Wednesday, 9am US Pacific
- Primary: Show HN with the launch piece URL (not the repo)
- Secondary: lobste.rs, Bluesky, X (in that order of expected impact)
- The author is in HN comments for 4 hours straight, no exceptions
- Cross-post to relevant subreddits 2–3 days later if HN landed

What not to do: Product Hunt, paid ads, "please upvote" posts, podcast
outreach, hashtag campaigns. None of these convert at this stage.

### 6.3 Post-launch

- Convert the launch tail by responding to every issue/PR within 48 hours
- Ship one technical post per month tied to a v0.2 scenario landing
- The compounding happens through repeat launches at v0.2 and v0.3,
  not through threads

### 6.4 Marketing budget

Modest, sustainable. The current allocation:

- Domain: ~$15/year amortized
- Plausible (analytics) when traffic is meaningful
- Buttondown (newsletter) when there's a v0.2 launch list to send to

The rest is reserve for trademark filing if the project earns it.

---

## 7. Operational rules

These are unchanged from `CLAUDE.md`:

- Sustainable time cadence; no death-march weekends.
- No new dependencies without explicit approval.
- Decisions favor shipping over polish.
- The OSS/Pro boundary is non-negotiable.
- Exit codes are part of the public contract.
- CLI surface is part of the public contract.
- No telemetry, ever.
- Apache 2.0 OSS, perpetual.

When a judgment call comes up that isn't covered, lean toward whatever
lets the current iteration ship sooner.

---

## 8. The closing honesty

This plan is a hypothesis, not a forecast. The AI-agent positioning
hasn't been tested by users yet. The cross-mode architecture is more
work than v0.1 strictly needs. The iteration cadence is a target, not
a promise.

That's fine. The plan is for shipping v0.1, not for being right about
everything in advance. v0.1 ships, the launch tests the positioning,
the data arrives, and the plan updates.

The strategic spine — small project, sustainable cadence, AI-agent
wedge, proxy + eBPF, ship over polish — is durable across a wide range
of outcomes. The tactics will change. That's the right ratio.

Build v0.1. Launch it. See what the world says.
