# flaky-network example

Tests the agent's TCP-level resilience — what happens when the kernel
returns `ECONNRESET` on a `recv` call.

## The bug

`agent.py`'s `fetch` opens a TCP connection, sends an HTTP request, and
reads the response. There is no retry on `ConnectionResetError`. When
the kernel surfaces an `ECONNRESET`, the exception propagates and the
caller sees a hard failure.

## Demo

```bash
pip install -r requirements.txt

pytest .                                                # passes

# Linux 5.8+ only:
faultkit run --scenario flaky-network -- pytest .       # fails under fault
```

Under fault injection, faultkit's eBPF program rewrites `recvmsg`
syscall return values to `-ECONNRESET` for processes inside the target
PID tree. The test sees a `ConnectionResetError` and fails.

## Fixing it

Add a retry loop with classification: `ECONNRESET` is transient — retry
with backoff. Track retry count; give up after a bounded number of
attempts. Distinguish from `ECONNREFUSED` (often permanent) and
`ETIMEDOUT` (sometimes worth retrying, sometimes not).
