# malformed-json-response example

Tests the agent's tool-calling robustness when the model returns
syntactically invalid JSON.

## The bug

The `decideTool` agent (in `python/agent.py` and `nodejs/agent.js`)
passes the model's content directly to `json.loads` / `JSON.parse`
and returns the result. No try/except, no schema validation. When
the model returns malformed JSON (a trailing comma, an unescaped
quote, a truncated structure), the parser raises and the exception
propagates through the agent loop.

## Demo

```bash
# Python
(cd python && pip install -r requirements.txt && pytest .)               # passes
faultkit run --config scenario.yaml -- python3 -m pytest python/         # fails

# Node
(cd nodejs && npm install && npm test)                                   # passes
faultkit run --config scenario.yaml -- node --test nodejs/test.js        # fails
```

The included `scenario.yaml` matches the local mock server and replaces
the response body with an OpenAI chat-completion envelope wrapping
intentionally-malformed inner JSON. Without validation, the parser
raises and the test fails.

## Fixing it

Validate the parsed shape against an expected schema. Catch
`json.JSONDecodeError` separately from `KeyError` — the former means
the model's output is malformed, the latter means the model returned
something well-formed but wrong. Both deserve different recovery
strategies.
