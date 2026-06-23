package proxy

import (
	"errors"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

func fixtureScenario(exps ...scenario.Experiment) *scenario.Scenario {
	return &scenario.Scenario{Name: "test", Experiments: exps}
}

func TestExpandAllProviders(t *testing.T) {
	s := fixtureScenario(scenario.Experiment{Name: "rl", Failure: "rate-limited", Probability: 0.2})
	got, err := expandScenario(s, "")
	if err != nil {
		t.Fatalf("expandScenario: %v", err)
	}
	if len(got.Experiments) != 2 {
		t.Fatalf("got %d experiments, want 2 (one per provider)", len(got.Experiments))
	}
	hosts := map[string]bool{}
	for _, e := range got.Experiments {
		hosts[e.Match.Host] = true
		if e.Fault.HTTPStatus != 429 {
			t.Errorf("exp %q HTTPStatus = %d, want 429", e.Name, e.Fault.HTTPStatus)
		}
		if e.Probability != 0.2 {
			t.Errorf("exp %q probability = %v, want 0.2", e.Name, e.Probability)
		}
	}
	for _, want := range []string{"api.openai.com", "api.anthropic.com"} {
		if !hosts[want] {
			t.Errorf("missing fan-out host %q (got %v)", want, hosts)
		}
	}
}

func TestExpandProviderField(t *testing.T) {
	s := fixtureScenario(scenario.Experiment{Name: "mj", Failure: "malformed-json", Provider: "anthropic", Probability: 0.15})
	got, err := expandScenario(s, "")
	if err != nil {
		t.Fatalf("expandScenario: %v", err)
	}
	if len(got.Experiments) != 1 {
		t.Fatalf("got %d experiments, want 1", len(got.Experiments))
	}
	e := got.Experiments[0]
	if e.Match.Host != "api.anthropic.com" {
		t.Errorf("host = %q, want api.anthropic.com", e.Match.Host)
	}
	if e.Match.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", e.Match.Path)
	}
	if e.Fault.ResponseBody == "" {
		t.Error("expected a verbatim malformed body")
	}
}

func TestExpandProviderScopeFilters(t *testing.T) {
	s := fixtureScenario(scenario.Experiment{Name: "rl", Failure: "rate-limited", Probability: 0.2})
	got, err := expandScenario(s, "openai")
	if err != nil {
		t.Fatalf("expandScenario: %v", err)
	}
	if len(got.Experiments) != 1 {
		t.Fatalf("got %d experiments, want 1 (scope=openai)", len(got.Experiments))
	}
	if got.Experiments[0].Match.Host != "api.openai.com" {
		t.Errorf("host = %q, want api.openai.com", got.Experiments[0].Match.Host)
	}
}

func TestExpandRawPassthrough(t *testing.T) {
	raw := scenario.Experiment{
		Name:        "custom",
		Fault:       faulttypes.Fault{HTTPStatus: 500},
		Match:       scenario.Match{Host: "example.com"},
		Probability: 1,
	}
	got, err := expandScenario(fixtureScenario(raw), "")
	if err != nil {
		t.Fatalf("expandScenario: %v", err)
	}
	if len(got.Experiments) != 1 || got.Experiments[0].Match.Host != "example.com" {
		t.Fatalf("raw experiment should pass through unchanged, got %+v", got.Experiments)
	}
}

func TestExpandUnknownMode(t *testing.T) {
	_, err := expandScenario(fixtureScenario(scenario.Experiment{Name: "x", Failure: "nope"}), "")
	if !errors.Is(err, errUnknownFailureMode) {
		t.Fatalf("err = %v, want errUnknownFailureMode", err)
	}
}

func TestExpandUnknownProvider(t *testing.T) {
	_, err := expandScenario(fixtureScenario(scenario.Experiment{Name: "x", Failure: "rate-limited", Provider: "bogus"}), "")
	if !errors.Is(err, errUnknownProvider) {
		t.Fatalf("err = %v, want errUnknownProvider", err)
	}
}

// Catalog provider ids must be real registered providers, else fan-out would
// silently skip them.
func TestCatalogProvidersAreRegistered(t *testing.T) {
	for _, mode := range fixtures.Modes() {
		for _, p := range providerRegistry {
			// every registered provider either has a fixture or not; the
			// inverse — a catalog provider with no registry entry — is the bug.
			_ = p
		}
		for _, id := range []string{"openai", "anthropic"} {
			if _, ok := fixtures.For(mode, id); ok {
				if _, found := providerByID(id); !found {
					t.Errorf("mode %q has a fixture for unregistered provider %q", mode, id)
				}
			}
		}
	}
}
