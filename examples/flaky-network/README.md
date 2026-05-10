# flaky-network example

Tests the agent's TCP-level resilience — what happens when the kernel
returns `ECONNRESET` on a `recv` call.

## The bug

The `fetch`/`fetchStatusLine` agent (in `python/agent.py` and
`nodejs/agent.js`) opens a TCP connection, sends an HTTP request,
and reads the response. There is no retry on `ECONNRESET`. When the
kernel surfaces a connection reset, the exception propagates and the
caller sees a hard failure.

## Demo

```bash
# Python
(cd python && pip install -r requirements.txt && pytest .)               # passes
faultkit run --scenario flaky-network -- python3 -m pytest python/       # fails

# Node
(cd nodejs && npm install && npm test)                                   # passes
faultkit run --scenario flaky-network -- node --test nodejs/test.js      # fails
```

Linux 5.8+ for the eBPF injector.

Under fault injection, faultkit's eBPF program rewrites `recvmsg`
syscall return values to `-ECONNRESET` for processes inside the target
PID tree. The test sees a `ConnectionResetError` and fails.

## Fixing it

Add a retry loop with classification: `ECONNRESET` is transient — retry
with backoff. Track retry count; give up after a bounded number of
attempts. Distinguish from `ECONNREFUSED` (often permanent) and
`ETIMEDOUT` (sometimes worth retrying, sometimes not).
