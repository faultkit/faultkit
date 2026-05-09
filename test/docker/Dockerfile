# syntax=docker/dockerfile:1

# Reproducible runner for `make test-integration`. Mirrors the
# toolchain matrix the example targets exercise: Go for faultkit
# itself, python3+pytest+openai for the proxy integration test, and
# Node 20 for the JS examples (added in a follow-up commit).
FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
        python3 python3-pip python3-venv \
        curl ca-certificates gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Pin so image rebuilds don't drift. Keep in sync with what the
# integration test expects in the host venv (/tmp/fk-venv on dev).
RUN pip install --break-system-packages --no-cache-dir \
        openai==2.36.0 \
        pytest==8.3.4

WORKDIR /workspace
COPY . .

CMD ["make", "test-integration"]
