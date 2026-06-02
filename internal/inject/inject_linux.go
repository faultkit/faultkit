//go:build linux

package inject

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Linux capability bit indices, see capabilities(7).
const (
	capNetAdmin = 12
	capPerfmon  = 38
	capBPF      = 39
)

func availableModes() []ModeReport {
	return []ModeReport{
		{Mode: ModeProxy, Available: true},
		ebpfModeReport(),
	}
}

func ebpfModeReport() ModeReport {
	// The BPF programs hook x86-64 syscall kprobes (__x64_sys_*) and are
	// built with __TARGET_ARCH_x86. On other arches the kprobe symbols
	// don't exist, so attach fails at run time — report it up front
	// instead of letting `check` claim eBPF works.
	if runtime.GOARCH != "amd64" {
		return ModeReport{Mode: ModeEBPF, Available: false, Reason: "needs x86-64 (programs hook __x64_sys_* kprobes)"}
	}
	if v := kernelRelease(); v != "" && !kernelAtLeast(v, 5, 8) {
		return ModeReport{Mode: ModeEBPF, Available: false, Reason: fmt.Sprintf("kernel %s < 5.8", v)}
	}
	if os.Geteuid() == 0 {
		return ModeReport{Mode: ModeEBPF, Available: true, Reason: "via root"}
	}
	if hasBPFCaps() {
		return ModeReport{Mode: ModeEBPF, Available: true, Reason: "via CAP_BPF + CAP_NET_ADMIN + CAP_PERFMON"}
	}
	return ModeReport{Mode: ModeEBPF, Available: false, Reason: "needs CAP_BPF + CAP_NET_ADMIN + CAP_PERFMON or root"}
}

func kernelRelease() string {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// kernelAtLeast parses the leading <major>.<minor> from version (which
// may look like "6.19.12-1-cachyos") and reports whether it meets the
// requested minimum.
func kernelAtLeast(version string, wantMajor, wantMinor int) bool {
	majStr, rest, ok := strings.Cut(version, ".")
	if !ok {
		return false
	}
	minStr, _, _ := strings.Cut(rest, ".")
	if i := strings.IndexFunc(minStr, notDigit); i >= 0 {
		minStr = minStr[:i]
	}

	maj, err := strconv.Atoi(majStr)
	if err != nil {
		return false
	}
	if maj != wantMajor {
		return maj > wantMajor
	}
	min, err := strconv.Atoi(minStr)
	if err != nil {
		return false
	}
	return min >= wantMinor
}

func notDigit(r rune) bool { return r < '0' || r > '9' }

// hasBPFCaps reports whether the effective capability set has the three
// caps eBPF mode needs. PR_SET_DUMPABLE (see ebpf_linux.go) covers the
// /proc/self/mem read at load, so CAP_SYS_PTRACE is intentionally not
// required here — verified on a real eBPF host under file caps.
func hasBPFCaps() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
		if err != nil {
			return false
		}
		return (mask&(1<<capNetAdmin)) != 0 &&
			(mask&(1<<capBPF)) != 0 &&
			(mask&(1<<capPerfmon)) != 0
	}
	return false
}
