from agent import ask


def test_ask_returns_answer(client):
    # Passes when the agent gets a normal completion; under faultkit's
    # llm-api-degraded fault the call raises a rate-limit error and this
    # fails — the bug the demo surfaces.
    answer = ask(client, "what is the answer?")
    assert "42" in answer
