import pytest
from openai import OpenAI


@pytest.fixture
def client():
    # The real production base_url. Under `faultkit run`, the injected
    # HTTPS_PROXY routes this through faultkit's MITM proxy and
    # SSL_CERT_FILE makes the SDK trust the per-run CA — no client config
    # at all, exactly as a user's agent would call OpenAI in production.
    #
    # Unlike the local-mock example (which uses an http:// loopback URL
    # and must mount a proxy transport because httpx bypasses the proxy
    # for localhost), api.openai.com is non-loopback, so the SDK's
    # default httpx honors HTTPS_PROXY on its own. The synthetic 429
    # fires before any upstream round trip, so the test never reaches the
    # real network.
    #
    # max_retries=0 keeps the run fast and deterministic: every request
    # is a 429, so we surface the failure immediately instead of waiting
    # out the SDK's default backoff.
    return OpenAI(
        api_key="test-key",
        base_url="https://api.openai.com/v1",
        max_retries=0,
    )
