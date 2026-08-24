package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCheckListsProviders(t *testing.T) {
	var buf bytes.Buffer
	if err := runCheck(&buf); err != nil {
		// runCheck errors only when no modes are available; proxy is always
		// available, so this should not happen on any host.
		t.Fatalf("runCheck: %v", err)
	}
	out := buf.String()
	// This branch is cut from main and does NOT include the Bedrock provider
	// (that lands on separate branches). Assert only the providers registered
	// here; Bedrock appears automatically in `check` once its branches merge,
	// with no change to this feature.
	for _, want := range []string{"providers:", "openai", "anthropic"} {
		if !strings.Contains(out, want) {
			t.Errorf("check output missing %q:\n%s", want, out)
		}
	}
}
