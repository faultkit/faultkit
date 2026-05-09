from agent import stream_answer


def test_stream_answer_completes(client):
    answer = stream_answer(client, "what is the answer?")
    assert answer.endswith(".")
    assert "42" in answer
