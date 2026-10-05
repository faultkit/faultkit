# FaultKit — Store Verification (`verify`) Design

**What this is:** the design and plan for checking a data store after a
fault run. Its status is **parked**: the design is agreed, but the feature is
not scheduled. It started as feedback from an early adopter running an agent
platform whose licensing and billing state lives in Postgres.

**When to pick it up:** when that adopter confirms they still need it after
trying the gate-test pattern in §3, or when a second user asks for it. Until
then, §3 is the answer.

**The guardrail:** this is a feature change under `V0.1.1_SPEC.md` §1. It adds
to the YAML scenario schema and to `report/v1`. It also changes one exit-code
case, described in §6. Every rule in `CLAUDE.md` stays in force.

---

## 1. Problem

faultkit's verdict comes from two facts: how many faults fired, and the
target's exit code. Many business invariants are about **persisted state**,
and the target never looks at it. Two examples:
- "no paid run starts for an organization whose license could not be
  resolved";
- "a hold never expires while spend is recorded".

So a run can pass while the store is wrong. The target exits 0 because the
API answered as expected, but the code behind it took the fault and wrote an
inconsistent row. That is exactly a silent failure, and today it shows up as
`invariant_proven_under_fault`.

## 2. Goals and non-goals

Goals:
- After the faults stop, check the store **from outside the fault
  environment**. A green target with a broken store becomes
  `silent_failure_confirmed`.
- Solve it **inside the faultkit binary**: no external command such as
  `psql`, no Go `plugin` package, and nothing downloaded at run time.
- Keep the contracts intact:
  - the scenario schema and `report/v1` only gain fields;
  - no new exit codes;
  - the one exit-code change in §6 is called out as breaking.

Non-goals for v1:
- more than one datastore (Postgres only);
- regex or scalar comparisons;
- a baseline run before the faults;
- retries for asynchronous writes;
- environment-conditional rules (§10, open);
- staging or long-running proxy modes, since faultkit stays single-shot;
- production use.

## 3. What works today, without this feature

The gate test can query the store itself after doing the work under the
fault:

```python
# tests/test_no_run_without_license.py (gate test)
def test_no_run_without_license(db):          # read-only connection
    run_assessment_flow()                      # the work done under the fault
    rows = db.execute("""SELECT r.id FROM runs r
                         JOIN usage u USING (run_id)
                         WHERE r.license_id IS NULL AND u.cost_usd > 0""").fetchall()
    assert rows == []
```

```bash
faultkit run --config .faultkit/invariants/no-run-without-license.yaml \
  -- pytest -q tests/test_no_run_without_license.py
```

What `verify` would add over this:
- a declarative check for teams whose tests are not in the same language as
  the store client;
- a single, read-only, timeout-bounded connection that faultkit owns;
- the result in `report/v1`.

The Postgres wire protocol never passes through faultkit's HTTP proxy, so a
gate-test query is already independent of the injected fault.

## 4. Scenario schema

`verify` is an optional top-level list in the scenario file, next to
`experiments`.

```yaml
name: no-run-without-license
experiments:
  - name: identity-no-license
    fault:
      http_status: 404
      response_headers: {Content-Type: application/json}
      response_body: '{"licenseId": null}'
    match: {host: "*", path: /organizations/*/license}
    probability: 1.0

verify:
  - name: no-paid-run-without-license
    postgres:
      dsn_env: VERIFY_DATABASE_URL
      query: |
        SELECT r.id
        FROM runs r
        JOIN usage u USING (run_id)
        WHERE r.license_id IS NULL AND u.cost_usd > 0
    timeout: 10s
```

| Field | Required | Meaning |
|---|---|---|
| `verify` | no | A list. If present, it has at least one item. |
| `verify[].name` | yes | kebab-case, unique within the list. Shown in the terminal summary and the report. |
| exactly one verifier key | yes | The key selects the verifier. v1 ships `postgres` only. |
| `postgres.dsn_env` | yes | The **name** of the environment variable that holds the connection URL, matching `^[A-Z_][A-Z0-9_]*$`. The value never appears in YAML. pgx fills missing parts from `PG*` variables and `~/.pgpass`. |
| `postgres.query` | yes | A **violation query**: it selects the rows that break the invariant, and the step passes only when it returns zero rows. It must be a single statement. |
| `timeout` | yes | A Go duration greater than 0. It bounds the connect and the query. |

Validation errors are usage errors (exit 4) and are raised before anything
runs:
- an empty list;
- a missing, duplicate, or non-kebab name;
- zero or several verifier keys;
- an unknown verifier key. The message names the verifiers this build
  supports, for example `this faultkit supports: postgres`;
- an invalid `dsn_env`;
- an empty `query`;
- a missing or non-positive `timeout`;
- `verify` without `experiments`.

The rule that registry-sourced scenarios may not carry `verify` is enforced
by the consumers (§8). Only they see the manifest's `source` field.

## 5. Execution

**When.** After the target exits and `inj.Stop` returns, and before the
summary and report are written, in `internal/cli/run.go`.
- If the scenario has `verify`, the steps always run, including when no
  fault fired or the target failed. The verdict table in §6 decides what
  they mean.
- If the target failed to start (an internal error), verify is skipped and
  the run exits 2.

**Where.** In faultkit's own process, through a Postgres verifier built on
`github.com/jackc/pgx/v5`. pgx is pure Go, so `CGO_ENABLED=0` builds keep
working. Adding it is a new dependency that needs approval when the feature
is unparked.

**Isolation by construction.** The runner gives the target
`append(os.Environ(), injected...)`. faultkit's own environment never
contains the proxy, CA bundle, or `*_BASE_URL` variables, so
`os.Getenv(dsn_env)` sees a clean value. eBPF faults are tied to the
target's PID, so faultkit's own connection is unaffected.

**Safety.**
- The session sets `default_transaction_read_only=on`, sets
  `statement_timeout` to the step's timeout, and sets
  `application_name=faultkit-verify`.
- The query runs inside `BEGIN READ ONLY … ROLLBACK`.
- faultkit counts rows (`rows.Next()`) and never reads column values.
- Each step opens and closes its own connection.
- Error text is truncated to 512 bytes, and the DSN is never printed.

**Order.** Steps run in file order, independently; one failing does not stop
the others.

**Output.** One terminal line per step, for example
`verify: no-paid-run-without-license   2 rows   FAIL`, followed by a
`verdict:` line. `--verbose` adds the connection target name and the
duration.

**Asynchronous writes.** v1 does not retry. The target is responsible for
waiting until its background work has finished.

## 6. Verdict and exit codes

The target **and** every verify step must pass. A target that crashes early
can therefore never produce a false "proven".

| Fault fired | Target exit | Verify | Verdict | Exit |
|---|---|---|---|---|
| no | any | any | `invalid_evidence` | 3 |
| yes | 0 | every step returns 0 rows | `invariant_proven_under_fault` | 0 |
| yes | 0 | any step returns rows | `silent_failure_confirmed` | 1 |
| yes | ≠0 | any (still run and reported) | `silent_failure_confirmed` | 1 |
| yes | any | a step could not run (env missing, connection, query error, timeout) | see §10 | 2 |

The exit code and the verdict come from **one function**. That fixes a
pre-existing mismatch:
- Today, when no fault fired and the target failed, `run.go` returns exit 1
  (`TargetExitError` is checked first).
- `verdictFor` reports `invalid_evidence` for the same run.
- After this change that case exits **3**.

This is a **breaking change** to the CLI contract and goes in the CHANGELOG.
It can also ship on its own, ahead of `verify`. The skills helper already
treats exit 1 with no faults fired as invalid evidence.

## 7. Report (`report/v1`, additive)

```json
"verify": [
  { "name": "no-paid-run-without-license", "verifier": "postgres",
    "violating_rows": 2, "passed": false, "duration_ms": 38 }
]
```

A step that could not run carries `"error": "<truncated message>"` instead
of `violating_rows`. The report never contains the DSN, the password, the
query text, or row data. The verdict `reason` names the failing step.

## 8. Consumers

The skills helper (`run_faultkit.py`) and faultkit/action each need four
changes:
- **A version gate.** The YAML loader is not strict: an older faultkit
  silently ignores `verify` and would report "proven" without checking. A
  scenario with `verify` needs the first faultkit that supports it, and the
  helper's `MIN_VERSION` moves to that release.
- **Declared but missing is an error.** If the scenario declares `verify` but
  the report has no `verify` section, the result is an error (exit 2).
- **Registry scenarios.** An entry with a `source` (a registry scenario) may
  not carry `verify`.
- **Rendering.** The proof block and the PR comment show each step, for
  example `verify: no-paid-run-without-license: 2 violating rows`.

The helper never connects to the database itself. This keeps credentials out
of the Claude plugin, whose directory review holds plugins that forward
credentials.

## 9. Code structure

| Unit | Responsibility |
|---|---|
| `pkg/scenario` | `Verify []VerifyStep` and its validation. This is a public API addition and needs explicit approval. |
| `internal/verify` | The `Verifier` interface, the registry (`init()` registration, the same pattern as built-in scenarios), and the step runner (timeouts, ordering, results). |
| `internal/verify/postgres` | The pgx verifier: config from `dsn_env`, a read-only transaction, row counting. |
| `internal/cli/run.go` | Runs verify after `inj.Stop`, and derives the verdict and exit from one function. |
| `internal/report` | The `verify` results in `Summary`, the terminal lines, the `verdictFor` update. |
| `cmd/faultkit` | Unchanged apart from a blank import of `internal/verify/postgres`. |

The interface stays in `internal/` for v1. It moves to `pkg/` only with the
compile-time extension work (§11), when an external consumer shapes it.

## 10. Open questions

1. **The verdict state when a step could not run.** Either add a fourth state
   (`error`), or write the report with no verdict. Today's three states don't
   cover it.
2. **Environment-dependent rules,** for example "a missing license is fine in
   dev but not in staging-like configurations". One option is query
   parameters taken from environment variables (`params: [APP_ENV]` → `$1`):
   no new outcome, and a missing variable is a loud error. The other is a
   per-step `when` clause, which adds a "skipped" outcome that could hide a
   misconfiguration.
3. **Staging and e2e.**
   - faultkit is single-shot and faults only the processes it starts. e2e
     works when faultkit wraps a script that starts the application as a
     child process, runs the suite, and stops it.
   - A deployed staging service needs a long-running proxy mode, which is out
     of scope (`CLAUDE.md`), or a verify-only mode paired with another fault
     tool. In that case faultkit cannot attest that the fault fired.

## 11. Extension path (compile-time)

1. **Verifiers (this spec).**
2. **Custom builds:**
   - a public entry point, such as a `pkg/` wrapper over `cli.Execute`;
   - promote the verifier interface to `pkg/`;
   - a small builder in the xcaddy / OpenTelemetry Collector builder style,
     so third parties compile their own verifiers into their own faultkit.
3. **Third-party fault injectors:** expose `Injector` through
   `pkg/extension`. This is the largest API commitment.

Rejected:
- **Go's `plugin` package.** It needs cgo and dynamic linking, while releases
  are static. Plugins must be built in lockstep with the host's exact
  toolchain and dependencies, and they run in-process, sometimes with eBPF
  privileges.
- **Downloading modules at run time.** This conflicts with the supply-chain
  rules and with the plugin directory's policy.

Deferred, if real demand appears:
- exec-based plugins (kubectl-style, or HashiCorp go-plugin);
- WASM through wazero.

## 12. Alternatives considered

| Alternative | Why not |
|---|---|
| An external `run:` command (the original feedback) | It needs client tools in the environment and forwards credentials to a subprocess. The decision was to keep the check inside the binary. |
| A separate checks file (`--verify checks.yaml`) | A new flag, an extra file per invariant, and a manifest bump in two consumers. It can be added later on top of the same block. |
| A separate `faultkit verify` subcommand | The verdict logic would be duplicated in the helper and the action, and nothing would enforce "after the faults stop". |
| MySQL and Redis drivers in v1 | More dependencies and maintenance, with no current user. |

## 13. Plan (when unparked)

1. **The exit and verdict fix (§6).** It can ship independently, flagged as
   breaking.
2. **Schema and validation** in `pkg/scenario`, with table-driven tests for
   every exit-4 rule.
3. **`internal/verify`:** the interface, registry, and step runner. Tested
   with a fake verifier for timeouts, ordering, and independence.
4. **`internal/verify/postgres`:** pgx (after dependency approval), the
   read-only transaction, and counting. Integration test behind
   `//go:build integration`, against a disposable Postgres.
5. **The `run.go` hook, the report fields, and the terminal lines.** Then
   end-to-end tests with a fake verifier:
   - a green target with a broken store gives exit 1;
   - a clean store gives 0;
   - no faults fired gives 3;
   - a step that could not run gives 2.
6. **Docs:** `yaml-schema.md` (a new section and a note under Versioning), the
   report docs, and the README.
7. **Consumers (§8):** a skills PR and an action PR.
8. **Release:** v0.2.0, because of the breaking exit-code change. Run
   `make lint test sec` before the PR, as `CLAUDE.md` requires.
