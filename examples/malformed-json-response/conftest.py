import pytest
from openai import OpenAI

from mock_server import serve_in_background


@pytest.fixture
def client():
    port, shutdown = serve_in_background()
    yield OpenAI(api_key="test-key", base_url=f"http://127.0.0.1:{port}/v1")
    shutdown()
