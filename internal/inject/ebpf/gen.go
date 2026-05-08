//go:build linux

// Package ebpf implements faultkit's syscall-level injector. The BPF
// programs live in bpf/ and are compiled by `go generate` (which
// invokes bpf2go and clang). The generated bindings are committed so
// end users don't need clang installed.
package ebpf

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -no-strip -target bpfel flakyNetwork ../../../bpf/flaky_network.bpf.c -- -I../../../bpf -O2 -Wall -D__TARGET_ARCH_x86
