# tool-permission-denied example

Tests the agent's tool-error handling — what happens when a file
operation fails with `EACCES`.

## The bug

`agent.py`'s `read_file_tool` uses a broad `except Exception` and
returns an empty string on any failure. An `EACCES` from the kernel
becomes `""` to the caller — indistinguishable from "the file exists
but is empty". The agent's reasoning loop has no way to know the file
read silently failed and proceeds with corrupt state.

## Demo

```bash
pip install -r requirements.txt

pytest .                                                       # passes

# Linux 5.8+ only:
faultkit run --scenario tool-permission-denied -- pytest .     # fails
```

Under fault injection, faultkit's eBPF program rewrites `openat`
syscall return values to `-EACCES`. The agent's tool returns `""`
instead of raising, and the test asserting actual content fails.

## Fixing it

Catch specific exceptions. `PermissionError` is distinct from
`FileNotFoundError`. Surface them differently to the caller — the
agent loop should know whether a tool said "I can't access that" vs
"that doesn't exist" vs "something unexpected went wrong". Returning
`""` as a "no result" signal collides with all three.
