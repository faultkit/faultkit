//go:build !linux

package inject

import "runtime"

func availableModes() []ModeReport {
	return []ModeReport{
		{Mode: ModeProxy, Available: true},
		{Mode: ModeEBPF, Available: false, Reason: "not supported on " + runtime.GOOS},
	}
}
