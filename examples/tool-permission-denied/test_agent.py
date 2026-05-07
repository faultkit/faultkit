from agent import read_file_tool


def test_reads_config(readable_file):
    content = read_file_tool(readable_file)
    assert content == "secret=42\n"
