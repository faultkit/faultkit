package inject

// Mode identifies a fault-injection mechanism.
type Mode string

// ModeProxy uses an HTTPS proxy with a per-run CA.
const ModeProxy Mode = "proxy"

// ModeEBPF uses kernel-side BPF programs to rewrite syscall return values.
const ModeEBPF Mode = "ebpf"

// ModeReport describes whether a Mode can run on this host.
type ModeReport struct {
	Mode      Mode
	Available bool
	// Reason is empty when Available is true and no qualifier applies;
	// otherwise it carries either a qualifier ("via root") or the
	// reason the mode is unavailable.
	Reason string
}

// AvailableModes returns the availability of every known mode on this host.
func AvailableModes() []ModeReport { return availableModes() }
