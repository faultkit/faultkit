# llm-streaming-cutoff example

Tests the agent's handling of streaming responses that drop mid-stream.

## The bug

`agent.py`'s `stream_answer` consumes chunks from the stream until the
iterator ends and returns the concatenated content. There is no check
for `finish_reason`, no detection of a missing `[DONE]` sentinel. A
network drop mid-stream produces a truncated string that the agent
treats as final.

## Demo

```bash
pip install -r requirements.txt

pytest .                                                # passes

# Under faultkit (lands in v0.1 phases 2–5):
faultkit run --config scenario.yaml -- pytest .         # fails
```

> **Note:** `scenario.yaml` is intentionally not yet shipped here. The
> `stream_cutoff_tokens` field needs to land in `pkg/faulttypes.Fault`
> first (Phase 1 follow-up per V0.1_SPEC v2). Once it does, the
> scenario looks like:
>
> ```yaml
> name: llm-streaming-cutoff-local
> experiments:
>   - name: drop-after-3-tokens
>     fault:
>       stream_cutoff_tokens: 3
>     match:
>       host: 127.0.0.1*
>       path: /v1/*
>     probability: 1.0
> ```

Under fault injection, the proxy forwards N tokens then closes the
connection without sending `[DONE]`. The agent's answer is truncated;
the test asserting it ends with "." fails.

## Fixing it

Track `finish_reason` from the final delta. Treat any stream that
ends without an explicit completion signal as incomplete. Either
retry, or surface partial output to the user as truncated rather than
authoritative.
