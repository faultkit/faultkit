SHELL := /bin/bash

VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

CLI_PKG := github.com/faultkit-dev/faultkit/internal/cli
LDFLAGS := -X $(CLI_PKG).version=$(VERSION) \
           -X $(CLI_PKG).commit=$(COMMIT) \
           -X $(CLI_PKG).date=$(DATE)

.PHONY: build test test-integration test-docker lint sec bpf clean

build:
	go build -ldflags '$(LDFLAGS)' -o bin/faultkit ./cmd/faultkit

test:
	go test ./...

# End-to-end tests against real client SDKs. Each test skips if its
# prereqs aren't met (e.g. python3+pytest+openai for the proxy test).
test-integration: build
	go test -tags integration ./test/integration/...

# Run the integration tests inside a reproducible container so
# contributors don't need python3+pytest+openai installed locally.
test-docker:
	docker build -f test/docker/Dockerfile -t faultkit-test:latest .
	docker run --rm faultkit-test:latest

lint:
	go vet ./...
	@diff=$$(gofmt -l .); \
	if [ -n "$$diff" ]; then \
		echo "gofmt issues in:"; echo "$$diff"; exit 1; \
	fi
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed; install with:"; \
		echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

# Pinned tool versions — see .github/workflows/ci.yml for the same pins.
# `@latest` would defeat the supply-chain hardening (GOTOOLCHAIN=local +
# pinned Go) one layer up.
GOSEC_VERSION       := v2.22.9
NILAWAY_VERSION     := v0.0.0-20260318203545-ad240b12fb4c
GOVULNCHECK_VERSION := v1.3.0

sec:
	@if ! command -v gosec >/dev/null 2>&1; then \
		echo "gosec not installed; install with:"; \
		echo "  go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)"; \
		exit 1; \
	fi
	@if ! command -v nilaway >/dev/null 2>&1; then \
		echo "nilaway not installed; install with:"; \
		echo "  go install go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VERSION)"; \
		exit 1; \
	fi
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck not installed; install with:"; \
		echo "  go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)"; \
		exit 1; \
	fi
	gosec ./...
	nilaway -include-pkgs=github.com/faultkit-dev/faultkit ./...
	govulncheck ./...

bpf: bpf/vmlinux.h
	go generate ./internal/inject/ebpf/...

bpf/vmlinux.h:
	bpftool btf dump file /sys/kernel/btf/vmlinux format c > $@

clean:
	rm -rf bin/ dist/
