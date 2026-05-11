import pytest


@pytest.fixture
def readable_file(tmp_path):
    p = tmp_path / "config.txt"
    p.write_text("secret=42\n")
    return str(p)
