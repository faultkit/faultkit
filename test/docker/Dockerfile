# syntax=docker/dockerfile:1

# Reproducible runner for `make test-integration`. Mirrors the
# toolchain matrix the example targets exercise: Go for faultkit
# itself, python3+pytest+openai for the proxy integration test, and
# Node 20 for the JS examples.
FROM golang:1.25.11-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
        python3 python3-pip python3-venv \
        curl ca-certificates gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Pinned in test/docker/requirements.txt so .github/workflows/ci.yml
# can install the same versions without a second pin site.
COPY test/docker/requirements.txt /tmp/fk-test-requirements.txt
RUN pip install --break-system-packages --no-cache-dir \
        -r /tmp/fk-test-requirements.txt

WORKDIR /workspace
COPY . .

# Pinned Node deps (package-lock.json) for the base-URL integration test;
# `npm ci` installs exactly the locked versions.
RUN cd examples/llm-api-degraded/nodejs && npm ci --no-audit --no-fund

CMD ["make", "test-integration"]
