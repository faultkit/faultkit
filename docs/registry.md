# Scenario registry

faultkit ships a small set of built-in scenarios and lets you write
your own as YAML. Beyond that, faultkit supports a **local scenario
registry** — a directory of scenario YAMLs you point faultkit at.
Anyone can publish a registry; the most common shape is a public
GitHub repo (see [Contributing](#contributing-a-registry) below).

The faultkit binary never fetches over the network. You `git clone`
(or `gh repo clone`) the registry and tell faultkit where it is.

---

## Using a registry

```bash
# Get the registry
gh repo clone faultkit/faultkit-scenarios ~/.local/share/faultkit/scenarios

# Run a scenario from it
faultkit run \
  --registry-root ~/.local/share/faultkit/scenarios \
  --scenario llm/api-degraded \
  -- pytest tests/agent/
```

The `--registry-root` flag (env: `FAULTKIT_REGISTRY_ROOT`) names the
local clone. `--scenario <name>` then resolves `<name>` in this
order:

1. **Filesystem path** — if `<name>` ends in `.yaml`, faultkit uses
   it as-is. Path wins; nothing else is searched behind it.
2. **Built-in scenario** — faultkit's own catalog. Builtins
   always win over the registry, even if a registry file
   shadows the same name. Rename the registry entry if you
   want the registry's copy to take precedence.
3. **Registry root** — when `--registry-root` is set, faultkit
   tries `<root>/<name>.yaml` and then `<root>/<name>/scenario.yaml`
   (the "pack-style" shape). First match wins.

A miss returns `ExitUsage` (4) with a clear message listing what was
checked.

### Listing

`faultkit scenario list` includes registry scenarios when
`--registry-root` is set. Each registry row is tagged `[registry]`
so it stands out from builtins:

```text
flaky-network            [ebpf]                Inject ECONNRESET on TCP recv operations.
llm-api-degraded         [proxy]               LLM provider returns 429/503/timeout under load.
llm/integration-scenario [proxy]   [registry]  an integration test scenario
tool-permission-denied   [ebpf]                File operations fail with EACCES.
```

### Validating a scenario file

Authors run `faultkit validate <file>` locally before opening a
PR. The same command is what the registry's CI shells out to on
every pull request. Exits 0 on success, `ExitUsage` (4) on any
schema or YAML problem with a clear explanation on stderr.

```bash
faultkit validate llm/integration-scenario.yaml
# ok: integration-scenario (llm/integration-scenario.yaml)
```

---

## Directory layout faultkit understands

A registry root is a directory. Scenarios live in two shapes, both
valid:

- **Single-file:** `<pack>/<name>.yaml` — the common case.
- **Pack-style:** `<pack>/<name>/scenario.yaml` plus an optional
  `README.md` in the same directory for longer writeups.

Packs are top-level category directories (e.g. `llm/`, `rag/`,
`tool-calls/`, `backend/`, `custom/`). faultkit doesn't enforce
pack names; they're a convention so users can browse by category.

When both `<name>.yaml` and `<name>/scenario.yaml` exist under the
same pack, the single-file shape wins.

---

## Contributing a registry

The recommended shape is a separate public GitHub repo. Concretely:

- A directory of `*.yaml` files, namespaced by category pack.
- An auto-generated `INDEX.md` at the root listing every scenario.
- CI that runs `faultkit validate` on every PR (pinned to a
  specific faultkit version) and posts the regenerated `INDEX.md`
  as a diff.
- A CLA-bot gate (Apache-2.0 individual CLA, the same one faultkit
  itself uses).

The scenario format is the existing faultkit YAML schema, unchanged
from `docs/yaml-schema.md`. No new format to learn.

Publishing is a GitHub PR. The faultkit binary never sees a
"publish" subcommand and never makes a network call on its own.

### Why no central hosted registry?

faultkit is built around three hard rules:

- No telemetry, no analytics, no "phone home" of any kind.
- The CLI is single-shot and offline.
- The OSS repo must not know about any specific registry, the Pro
  repo, or any commercial seam.

A hosted registry would break at least the first two. A local
registry pointed at by a directory path fits all three: the binary
stays offline, the user is in control of what they pull, and the
loader takes a generic path with no awareness of any specific
registry.

Pro can later add a private-registry feature on the
`pkg/extension` seam. The OSS loader does not change.
