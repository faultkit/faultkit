package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// White-box: origin mode reuses unexported faulter/registry internals.

func newOriginTestHandler(t *testing.T, s *scenario.Scenario) (*originHandler, chan inject.Event) {
	t.Helper()
	events := make(chan inject.Event, 8)
	return newOriginHandler(NewFaulter(s, events, nil), nil), events
}

// A 100%-probability HTTP fault on the upstream host is synthesized
// directly, without any upstream round trip.
func TestOriginSyntheticFault(t *testing.T) {
	s := &scenario.Scenario{
		Name: "origin-429",
		Experiments: []scenario.Experiment{{
			Name:        "openai-429",
			Fault:       faulttypes.Fault{HTTPStatus: 429},
			Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/*"},
			Probability: 1,
		}},
	}
	h, events := newOriginTestHandler(t, s)

	req := httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get(syntheticHeader) != "true" {
		t.Errorf("missing %s header; response was not synthesized by faultkit", syntheticHeader)
	}

	select {
	case ev := <-events:
		if !ev.Fired || ev.Host != "api.openai.com" {
			t.Errorf("event = %+v, want fired on api.openai.com", ev)
		}
	default:
		t.Error("expected a fired fault event")
	}
}

// A path with no provider prefix is rejected (it is not API traffic we
// know how to route in origin mode).
func TestOriginUnknownPath(t *testing.T) {
	h, _ := newOriginTestHandler(t, &scenario.Scenario{Name: "empty"})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unrecognized base-URL path", rec.Code)
	}
}
