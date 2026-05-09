from agent import ask


def test_ask_returns_answer(client):
    answer = ask(client, "what is the answer?")
    assert "42" in answer
