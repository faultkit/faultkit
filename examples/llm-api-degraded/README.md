# llm-api-degraded example

Tests the agent's ability to handle LLM provider rate-limit errors.

## The bug

`agent.py` calls `chat.completions.create` and returns the assistant's
content. There is no retry on `RateLimitError`, no backoff, no
fallback. When the provider returns 429, the exception propagates and
the agent fails the request.

## Demo

```bash
pip install -r requirements.txt

pytest .                                                  # passes

# Under faultkit (lands in v0.1 phases 2–5):
faultkit run --config scenario.yaml -- pytest .           # fails
```

The included `scenario.yaml` matches the local mock server (host
`127.0.0.1*`) and fires a 429 with `Retry-After: 30` on every matching
request. Without retry logic, the test fails on the first 429.

The builtin `llm-api-degraded` scenario targets `api.openai.com` and is
the right fit when the agent is pointed at the real OpenAI API. Use
the local `scenario.yaml` for this self-contained demo.

## Fixing it

Wrap the call in a retry-with-exponential-backoff loop, or use
openai-python's built-in `max_retries`. The interesting question
faultkit can answer: does your retry preserve correct reasoning state
when it fires? A retry that re-uses partially-built state silently
produces confidently-wrong answers — that's the production bug worth
finding.
