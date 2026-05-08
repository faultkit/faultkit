SHELL := /bin/bash

VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

CLI_PKG := github.com/faultkit-dev/faultkit/internal/cli
LDFLAGS := -X $(CLI_PKG).version=$(VERSION) \
           -X $(CLI_PKG).commit=$(COMMIT) \
           -X $(CLI_PKG).date=$(DATE)

.PHONY: build test lint sec bpf clean

build:
	go build -ldflags '$(LDFLAGS)' -o bin/faultkit ./cmd/faultkit

test:
	go test ./...

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

sec:
	@if ! command -v gosec >/dev/null 2>&1; then \
		echo "gosec not installed; install with:"; \
		echo "  go install github.com/securego/gosec/v2/cmd/gosec@latest"; \
		exit 1; \
	fi
	@if ! command -v nilaway >/dev/null 2>&1; then \
		echo "nilaway not installed; install with:"; \
		echo "  go install go.uber.org/nilaway/cmd/nilaway@latest"; \
		exit 1; \
	fi
	gosec ./...
	nilaway -include-pkgs=github.com/faultkit-dev/faultkit ./...

bpf: bpf/vmlinux.h
	go generate ./internal/inject/ebpf/...

bpf/vmlinux.h:
	bpftool btf dump file /sys/kernel/btf/vmlinux format c > $@

clean:
	rm -rf bin/ dist/
