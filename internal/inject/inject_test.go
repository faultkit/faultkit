package inject_test

import (
	"runtime"
	"testing"

	"github.com/faultkit/faultkit/internal/inject"
)

func TestAvailableModesIncludesBothKnown(t *testing.T) {
	got := inject.AvailableModes()
	seen := map[inject.Mode]bool{}
	for _, r := range got {
		seen[r.Mode] = true
	}
	for _, want := range []inject.Mode{inject.ModeProxy, inject.ModeEBPF} {
		if !seen[want] {
			t.Errorf("AvailableModes missing %q (got %+v)", want, got)
		}
	}
}

func TestProxyAlwaysAvailable(t *testing.T) {
	for _, r := range inject.AvailableModes() {
		if r.Mode == inject.ModeProxy && !r.Available {
			t.Errorf("proxy mode should always be available; got %+v", r)
		}
	}
}

func TestEBPFUnavailableOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux-specific check inverted; eBPF availability depends on caps")
	}
	for _, r := range inject.AvailableModes() {
		if r.Mode == inject.ModeEBPF && r.Available {
			t.Errorf("eBPF should be unavailable on %s; got %+v", runtime.GOOS, r)
		}
	}
}
