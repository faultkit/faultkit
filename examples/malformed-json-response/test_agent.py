from agent import decide_tool


def test_decide_tool_returns_expected_shape(client):
    decision = decide_tool(client, "find user 42")
    assert decision["tool"] == "lookup_user"
    assert decision["args"]["id"] == 42
