//go:build linux

// Package ebpf implements faultkit's syscall-level injector. The BPF
// programs live in bpf/ and are compiled by `go generate` (which
// invokes bpf2go and clang). The generated bindings are committed so
// end users don't need clang installed.
package ebpf

// __TARGET_ARCH_x86 is hardcoded; an arm64 build would need separate
// generate stanzas (different macro + kprobes renamed to __arm64_sys_*).
// Out of scope for v0.1.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -no-strip -target bpfel flakyNetwork ../../../bpf/flaky_network.bpf.c -- -I../../../bpf -O2 -Wall -D__TARGET_ARCH_x86
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -no-strip -target bpfel toolPermDenied ../../../bpf/tool_perm_denied.bpf.c -- -I../../../bpf -O2 -Wall -D__TARGET_ARCH_x86
