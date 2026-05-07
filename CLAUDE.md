# CLAUDE.md

Operating manual for Claude Code working on faultkit. Read this before any
session. The rules here override default behavior; they exist because this
project has specific decisions that aren't obvious from the code alone.

---

## Karpathy Guidelines

Behavioral guidelines to reduce common LLM coding mistakes, derived from
[Andrej Karpathy's observations](https://x.com/karpathy/status/2015883857489522876)
on LLM coding pitfalls.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial
tasks, use judgment.

### 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them — don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If
yes, simplify.

### 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it — don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: every changed line should trace directly to the user's request.

### 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:

```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make
it work") require constant clarification.

---

## Project-specific rules

These are non-negotiable. They encode architectural decisions that aren't
re-litigated session to session.

### The OSS/Pro boundary

faultkit is split across two repos:

- `github.com/faultkit-dev/faultkit` — public, Apache 2.0, this repo
- `github.com/faultkit-dev/faultkit-pro` — private, commercial license, separate

**The OSS repo must contain zero Pro-aware code.** No Pro mentions in
comments, no Pro feature flags, no `if isPro` branches, no stub commands
that say "this is a Pro feature." The OSS code must not know Pro exists.

The seam is `pkg/extension`. It is generic — Pro is one of many possible
wrappers. If you find yourself adding Pro-specific affordances to OSS
code, stop. The right answer is to add a generic capability to
`pkg/extension` that the Pro repo (which you don't have access to) uses.

If you're unsure whether something is Pro: it probably is. Ask before
adding.

### What goes where

- `cmd/faultkit/` — entry point only, ≤20 lines of substance.
- `pkg/` — public API. Stable, semver'd, importable by external code.
  Adding here is a commitment. Don't expand without asking.
- `internal/` — implementation detail. Refactor freely within rules.
- `bpf/` — eBPF C source, GPL-licensed (kernel requirement). Different
  rules apply (see "eBPF track" below).
- `shim/` — userspace LD_PRELOAD/DYLD library. Empty until v0.2.

If you can't immediately tell whether something is `pkg` or `internal`,
it's `internal`. Promotion to `pkg` requires explicit discussion.

### Build and test commands

```
make build          # OSS build
make test           # unit tests
make lint           # vet + gofmt + golangci-lint
make sec            # gosec + nilaway (phase-completion gate)
make bpf            # compile BPF programs (Linux + clang required)
```

Always run `make lint test` before declaring a task done. Not "I think it
should pass" — actually run it.

### Phase completion

When finishing a phase from `docs/internal/V0.1_SPEC.md`, run `make sec`
in addition to `make lint test`. Both `gosec` and `nilaway` must report
zero findings before the phase counts as done.

If a finding is genuinely a false positive:
- gosec: suppress at the call site with
  `// #nosec <RuleID> -- <reason>` and explain in the same line.
- nilaway: prefer fixing the code (often a small refactor); reach for
  `//nolint:nilaway` only when the false positive is rooted in the
  stdlib and the fix would mean defensive code for an impossible case.

Each suppression is a small documented decision. Don't blanket-suppress.

`gosec` and `nilaway` are external tools, not in `go.mod`. Install once:

```
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install go.uber.org/nilaway/cmd/nilaway@latest
```

`nilaway` is invoked with `-include-pkgs=github.com/faultkit-dev/faultkit`
to filter out stdlib-rooted nil flows (e.g. `os.Args` is technically
nilable but in practice never is). The Makefile target handles this
automatically.

### Dependencies

Current direct dependencies are pinned in `go.mod`. **Do not add new
dependencies without explicit approval.** This includes test-only deps,
linting deps, anything. Ask first; the answer is often "use the stdlib."

The dependencies we have:
- `github.com/cilium/ebpf` — eBPF loader
- `github.com/spf13/cobra` — CLI framework
- `github.com/spf13/viper` — config (used sparingly)
- `gopkg.in/yaml.v3` — YAML parsing (NOT v2)

### Error handling

- Always wrap with `fmt.Errorf("doing thing: %w", err)` — context plus
  unwrap. Bare `return err` is rarely correct.
- Sentinel errors are exported as `var ErrFoo = errors.New(...)` and
  matched with `errors.Is`.
- Typed errors (e.g. `*runner.TargetExitError`) are matched with
  `errors.As`. The CLI's exit code dispatch in `internal/cli/cli.go`
  depends on this — preserve the typed-error contract.
- Do not swallow errors silently. If you genuinely need to ignore one,
  explain why in a comment.

### Testing conventions

- Black-box tests by default: `package foo_test`, not `package foo`.
- One behavior per test. Table-driven tests when there's repetition.
- Don't test the standard library. Don't test cobra's flag parsing.
  Test our logic.
- Integration tests live in `test/integration/` with `//go:build integration`.
  They require Linux + privileges and run separately.

### CLI surface stability

The CLI is part of the user contract. Exit codes (`internal/cli/cli.go`),
flag names, command names, and YAML scenario schema are all promises.
Don't change them without flagging it as a breaking change. The exit
code constants `ExitOK`, `ExitTargetFailed`, `ExitInternalError`,
`ExitFaultNotFired`, `ExitUsage` are particularly load-bearing — CI
scripts branch on them.

### What is explicitly out of scope

These are deferred or refused. Don't propose them, don't scaffold them,
don't add code "in case we need it later":

- Kubernetes operators, CRDs, Helm charts
- Production fault injection (different blast-radius requirements)
- Per-language SDKs (Python, Node, etc.) — CLI + YAML covers it
- Interactive TUI mode
- Long-running daemon / serve mode (we are single-shot)
- Per-feature license keys, license validation logic
- BSL/SSPL/Elastic License experiments
- Telemetry, analytics, "phone home" of any kind

If a request leans into any of these, surface the scope question before
writing code.

### eBPF track — special handling

The `bpf/` directory and `internal/inject/ebpf/` are higher-risk
territory:

- BPF programs are C with verifier constraints that aren't in the source.
  Code that looks correct can be rejected for reasons no compiler will
  flag. Don't trust your output until the verifier accepts it.
- Tracepoint signatures vary across kernel versions. Use CO-RE
  (`bpf_core_read`, BTF) when reading struct fields. Hard-coded offsets
  will break on the user's kernel.
- The license header on every `.bpf.c` file must be:
  `// SPDX-License-Identifier: GPL-2.0` (kernel requirement; non-GPL
  programs can't call certain helpers).
- Before claiming an eBPF change works: load it, run it, verify with
  `bpftool prog list`. "It compiles" is not "it works."

When working in this area, default to asking before guessing. The cost
of a wrong assumption is much higher here than in Go code.

### Style and idioms

- `gofmt` and `goimports` formatting. Group imports: stdlib, third-party,
  internal — separated by blank lines.
- Receiver names are short and consistent (`r *Runner`, not `runner *Runner`).
- Exported names get full doc comments starting with the name. Unexported
  names get comments only when behavior is non-obvious.
- No init() functions for anything other than registry registration
  (the stock scenarios in `internal/scenario/builtin/` are the canonical
  pattern). Do not add init() that does I/O, network, or mutates global
  state for other reasons.
- Prefer accepting interfaces, returning structs.
- Context is the first argument when present, never stored in a struct.

### What to do when stuck

If you're stuck for more than two attempts on the same problem, stop and
surface it. State:

1. What you're trying to accomplish.
2. What you've tried.
3. What's blocking — error message, observed behavior, the gap between
   expected and actual.
4. Two or three options for proceeding, with tradeoffs.

Don't loop on the same wrong approach. Don't fabricate confidence. Don't
add layers of defensive code to work around a problem you don't
understand. Stop and ask.

### Commits

- One logical change per commit.
- Commit message format: short imperative subject (under 72 chars),
  blank line, body explaining *why* not *what*.
- Sign-off (`-s`) is not required (we use CLA, not DCO), but commits
  must still be made under your real identity.
- Never commit to `main` directly. PR-only.

### What this project is for (context for judgment calls)

faultkit is a side project with three goals: personal brand, evaluate
the OSS-core game, see commercial potential. It is NOT a venture-backed
startup. Decisions favor: shipping over polish, narrow scope over
completeness, individual developer experience over enterprise
checklists, two weekends a month of total effort.

When a judgment call comes up that isn't covered by these rules, lean
toward whatever lets a small team ship a v0.1 sooner. Don't over-engineer
for hypothetical scale.
