package proxy_test

import (
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// A real request is counted; the CONNECT that establishes the MITM tunnel
// is not. Drives the CLI's "no traffic reached faultkit" warning (T10).
func TestFaulterCountsRequests(t *testing.T) {
	f := proxy.NewFaulter(&scenario.Scenario{Name: "empty"}, nil, nil)
	if f.Seen() != 0 {
		t.Fatalf("Seen() = %d before any request, want 0", f.Seen())
	}
	_ = f.ModifyRequest(mustReq(t, "https://api.openai.com/v1/x"))
	connect := mustReq(t, "https://api.openai.com/v1/x")
	connect.Method = http.MethodConnect
	_ = f.ModifyRequest(connect)
	if f.Seen() != 1 {
		t.Errorf("Seen() = %d, want 1 (the CONNECT tunnel is not a request)", f.Seen())
	}
}

func httpScenario(probability float64) *scenario.Scenario {
	return &scenario.Scenario{
		Name: "faulter-test",
		Experiments: []scenario.Experiment{{
			Name:        "openai-rate-limited",
			Fault:       faulttypes.Fault{HTTPStatus: 429, ResponseHeaders: map[string]string{"Retry-After": "30"}},
			Match:       scenario.Match{Host: "api.openai.com"},
			Probability: probability,
		}},
	}
}

func newPCG() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestFaulterDecideFiresAtProbabilityOne(t *testing.T) {
	f := proxy.NewFaulter(httpScenario(1.0), nil, newPCG())
	req := mustReq(t, "https://api.openai.com/v1/chat/completions")
	exp := f.Decide(req)
	if exp == nil {
		t.Fatal("expected fire")
	}
	if exp.Name != "openai-rate-limited" {
		t.Errorf("got %q, want openai-rate-limited", exp.Name)
	}
}

func TestFaulterDecideNeverFiresAtProbabilityZero(t *testing.T) {
	f := proxy.NewFaulter(httpScenario(0), nil, newPCG())
	for i := 0; i < 100; i++ {
		req := mustReq(t, "https://api.openai.com/v1/chat/completions")
		if exp := f.Decide(req); exp != nil {
			t.Fatalf("iter %d: got %v, want nil", i, exp)
		}
	}
}

func TestFaulterDecideUnmatchedReturnsNil(t *testing.T) {
	f := proxy.NewFaulter(httpScenario(1.0), nil, newPCG())
	req := mustReq(t, "https://other.example.com/")
	if exp := f.Decide(req); exp != nil {
		t.Errorf("got %v, want nil for non-matching host", exp)
	}
}

func TestFaulterDeterministicWithSeed(t *testing.T) {
	s := httpScenario(0.5)
	const N = 200

	f1 := proxy.NewFaulter(s, nil, rand.New(rand.NewPCG(42, 0)))
	f2 := proxy.NewFaulter(s, nil, rand.New(rand.NewPCG(42, 0)))

	for i := 0; i < N; i++ {
		r := mustReq(t, "https://api.openai.com/v1/chat/completions")
		d1 := f1.Decide(r)
		d2 := f2.Decide(r)
		if (d1 == nil) != (d2 == nil) {
			t.Fatalf("iter %d: decisions diverged: %v vs %v", i, d1, d2)
		}
	}
}

func TestFaulterApproximatesProbability(t *testing.T) {
	const (
		N    = 2000
		prob = 0.3
		eps  = 0.05
	)
	f := proxy.NewFaulter(httpScenario(prob), nil, rand.New(rand.NewPCG(123, 456)))

	fired := 0
	for i := 0; i < N; i++ {
		r := mustReq(t, "https://api.openai.com/v1/chat/completions")
		if f.Decide(r) != nil {
			fired++
		}
	}
	rate := float64(fired) / float64(N)
	if rate < prob-eps || rate > prob+eps {
		t.Errorf("fire rate %.3f outside [%.2f, %.2f]", rate, prob-eps, prob+eps)
	}
}
