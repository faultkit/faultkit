"""
A file-reading tool an agent might invoke. The bug: the broad
`except Exception` clause swallows EACCES along with everything else
and returns an empty string. The agent's loop can't distinguish
"file empty" from "permission denied".
"""


def read_file_tool(path: str) -> str:
    try:
        with open(path) as f:
            return f.read()
    except Exception:
        return ""
