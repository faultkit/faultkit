import os

import httpx
import pytest
from openai import OpenAI

from mock_server import serve_in_background


def _proxy_aware_http_client():
    # httpx skips proxy for localhost by default, so under faultkit run
    # the local mock would be reached directly without going through
    # HTTPS_PROXY. Mounting the proxy transport for `all://` forces every
    # request through it. In production (api.openai.com over real
    # network) this isn't needed — the SDK's default httpx handles
    # the proxy correctly because the host is non-loopback.
    proxy = os.environ.get("HTTPS_PROXY") or os.environ.get("HTTP_PROXY")
    if not proxy:
        return None
    return httpx.Client(
        mounts={"all://": httpx.HTTPTransport(proxy=proxy)},
    )


@pytest.fixture
def client():
    port, shutdown = serve_in_background()
    yield OpenAI(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{port}/v1",
        http_client=_proxy_aware_http_client(),
        max_retries=0,
    )
    shutdown()
