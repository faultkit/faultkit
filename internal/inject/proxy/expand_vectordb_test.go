package proxy

import (
	"context"
	"strings"
	"testing"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
	"github.com/faultkit/faultkit/pkg/scenario"
)

func loadRAGStaleResults(t *testing.T) *scenario.Scenario {
	t.Helper()
	s, err := scenario.LoadBuiltin("rag-stale-results")
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	return s
}

// rag-stale-results fans out to exactly the two vector-DB providers, each on
// its query endpoint, with a 200 carrying the canned body.
func TestExpandRAGStaleResults(t *testing.T) {
	exp, err := expandScenario(loadRAGStaleResults(t), "")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	want := map[string]string{
		"*.pinecone.io":     "/query",
		"*.cloud.qdrant.io": "/collections/*/points/query",
	}
	if len(exp.Experiments) != len(want) {
		t.Fatalf("got %d experiments, want %d: %+v", len(exp.Experiments), len(want), exp.Experiments)
	}
	for _, e := range exp.Experiments {
		path, ok := want[e.Match.Host]
		if !ok {
			t.Errorf("unexpected host %q", e.Match.Host)
			continue
		}
		if e.Match.Path != path {
			t.Errorf("host %s: path = %q, want %q", e.Match.Host, e.Match.Path, path)
		}
		if e.Fault.HTTPStatus != 200 || e.Fault.ResponseBody == "" {
			t.Errorf("host %s: fault = %+v, want a 200 with the canned body", e.Match.Host, e.Fault)
		}
	}
}

// The expanded experiments fire on real index/cluster hosts — the documented
// host forms, and Qdrant Cloud's REST port, which normalizeHost strips — and
// only on the query endpoint.
func TestMatcherFiresOnVectorDBHosts(t *testing.T) {
	exp, err := expandScenario(loadRAGStaleResults(t), "")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	m := NewMatcher(exp)
	cases := []struct {
		host, path string
		want       bool
	}{
		{"idx-abc.svc.aped-1.pinecone.io", "/query", true},
		{"idx-abc.svc.aped-1.pinecone.io:443", "/query", true},
		{"xyz.eu-central.aws.cloud.qdrant.io:6333", "/collections/docs/points/query", true},
		{"xyz.eu-central.aws.cloud.qdrant.io", "/collections/docs/points/query", true},
		// Documented host forms (Pinecone list_indexes, Qdrant Cloud auth docs).
		{"example-index-fa77d8e.svc.aped-4627-b74a.pinecone.io", "/query", true},
		{"xyz-example.cloud-region.cloud-provider.cloud.qdrant.io:6333", "/collections/docs/points/query", true},
		// Other endpoints have a different response shape; never faulted.
		{"idx-abc.svc.aped-1.pinecone.io", "/vectors/fetch", false},
		{"xyz.eu-central.aws.cloud.qdrant.io:6333", "/collections/docs/points/query/batch", false},
		{"xyz.eu-central.aws.cloud.qdrant.io:6333", "/collections/docs/points/search", false},
		// Hosts that only contain a provider's domain aren't the provider.
		{"pinecone.io.example.com", "/query", false},
		{"qdrant.example.com", "/collections/docs/points/query", false},
		// Self-hosted Qdrant isn't a cloud host.
		{"localhost:6333", "/collections/docs/points/query", false},
	}
	for _, c := range cases {
		if got := m.matchHostPath(c.host, c.path) != nil; got != c.want {
			t.Errorf("matchHostPath(%q, %q) fired=%v, want %v", c.host, c.path, got, c.want)
		}
	}
}

func TestExpandRAGStaleResultsProviderScope(t *testing.T) {
	exp, err := expandScenario(loadRAGStaleResults(t), "qdrant")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(exp.Experiments) != 1 || exp.Experiments[0].Match.Host != "*.cloud.qdrant.io" {
		t.Errorf("--provider qdrant expanded to %+v, want only the qdrant experiment", exp.Experiments)
	}
	if _, err := expandScenario(loadRAGStaleResults(t), "openai"); err == nil {
		t.Error("--provider openai: want an error (stale-results has no openai fixture)")
	}
}

// The vector-DB providers are forward-proxy only, so base-URL mode has
// nothing to inject for rag-stale-results and must refuse to start rather
// than run a scenario that can never fire.
func TestBaseURLModeRefusesRAGStaleResults(t *testing.T) {
	inj := New()
	inj.UseBaseURL(true)
	env, err := inj.Start(context.Background(), loadRAGStaleResults(t))
	if err == nil {
		_ = inj.Stop(context.Background())
		t.Fatalf("Start in base-URL mode succeeded (env %v), want an error", env)
	}
	if !strings.Contains(err.Error(), "base-URL mode supports none of this scenario's hosts") {
		t.Errorf("Start error = %v, want the no-supported-hosts refusal", err)
	}
}
