# llm-streaming-cutoff example

Tests the agent's handling of streaming responses that drop mid-stream.

## The bug

The `streamAnswer` agent (in `python/agent.py` and `nodejs/agent.js`)
consumes chunks from the stream until the iterator ends and returns
the concatenated content. There is no check for `finish_reason`, no
detection of a missing `[DONE]` sentinel. A network drop mid-stream
produces a truncated string that the agent treats as final.

## Demo

```bash
# Python
(cd python && pip install -r requirements.txt && pytest .)               # passes
faultkit run --config scenario.yaml -- python3 -m pytest python/         # fails

# Node
(cd nodejs && npm install && npm test)                                   # passes
faultkit run --config scenario.yaml -- node --test nodejs/test.js        # fails
```

Under fault injection, the proxy forwards N tokens then closes the
connection without sending `[DONE]`. The agent's answer is truncated;
the test asserting it ends with "." fails.

## Fixing it

Track `finish_reason` from the final delta. Treat any stream that
ends without an explicit completion signal as incomplete. Either
retry, or surface partial output to the user as truncated rather than
authoritative.
