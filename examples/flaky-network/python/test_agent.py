from agent import fetch


def test_fetch_status_line(local_server):
    host, port = local_server
    line = fetch(host, port, "/")
    assert "200" in line
