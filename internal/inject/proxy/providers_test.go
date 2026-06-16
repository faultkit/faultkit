package proxy

import "testing"

// White-box: the provider registry is internal plumbing for base-URL mode,
// not part of the public API, so it's tested in-package.

func TestProviderBaseURL(t *testing.T) {
	openai := providersForHostGlobs([]string{"api.openai.com"})
	if len(openai) != 1 {
		t.Fatalf("expected the openai provider, got %d", len(openai))
	}
	got := openai[0].baseURL("127.0.0.1:8080")
	want := "http://127.0.0.1:8080/__fk/openai"
	if got != want {
		t.Errorf("baseURL = %q, want %q", got, want)
	}
}

func TestProviderForPath(t *testing.T) {
	tests := []struct {
		path     string
		wantID   string
		wantRest string
		wantOK   bool
	}{
		{"/__fk/openai/v1/chat/completions", "openai", "/v1/chat/completions", true},
		{"/__fk/anthropic/v1/messages", "anthropic", "/v1/messages", true},
		{"/__fk/openai", "openai", "/", true},   // bare prefix
		{"/__fk/openainot/v1", "", "", false},   // prefix must be a path segment
		{"/v1/chat/completions", "", "", false}, // no prefix
		{"", "", "", false},
	}
	for _, tt := range tests {
		p, rest, ok := providerForPath(tt.path)
		if ok != tt.wantOK {
			t.Errorf("providerForPath(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if p.id != tt.wantID || rest != tt.wantRest {
			t.Errorf("providerForPath(%q) = (%s, %q), want (%s, %q)", tt.path, p.id, rest, tt.wantID, tt.wantRest)
		}
	}
}

func TestProvidersForHostGlobs(t *testing.T) {
	ids := func(ps []provider) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.id
		}
		return out
	}

	if got := ids(providersForHostGlobs([]string{"api.openai.com"})); len(got) != 1 || got[0] != "openai" {
		t.Errorf("exact openai glob = %v, want [openai]", got)
	}
	if got := ids(providersForHostGlobs([]string{"api.*.com"})); len(got) != 2 {
		t.Errorf("wildcard glob = %v, want both providers", got)
	}
	if got := ids(providersForHostGlobs([]string{"example.com"})); len(got) != 0 {
		t.Errorf("non-matching glob = %v, want none", got)
	}
	if got := ids(providersForHostGlobs([]string{""})); len(got) != 0 {
		t.Errorf("empty glob = %v, want none", got)
	}
}
