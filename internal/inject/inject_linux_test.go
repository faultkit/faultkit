//go:build linux

package inject

import "testing"

func TestKernelAtLeast(t *testing.T) {
	cases := []struct {
		name      string
		version   string
		wantMajor int
		wantMinor int
		want      bool
	}{
		{"newer major", "6.0", 5, 8, true},
		{"exact match", "5.8", 5, 8, true},
		{"newer minor", "5.9.0", 5, 8, true},
		{"older minor", "5.7.99", 5, 8, false},
		{"older major", "4.19", 5, 8, false},
		{"distro suffix", "6.19.12-1-cachyos", 5, 8, true},
		{"single dot", "5", 5, 8, false},
		{"garbage", "not-a-version", 5, 8, false},
		{"empty", "", 5, 8, false},
		{"non-numeric minor", "5.x", 5, 8, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := kernelAtLeast(c.version, c.wantMajor, c.wantMinor)
			if got != c.want {
				t.Errorf("kernelAtLeast(%q, %d, %d) = %v, want %v", c.version, c.wantMajor, c.wantMinor, got, c.want)
			}
		})
	}
}
