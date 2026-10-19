package proxy_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/faultkit/faultkit/pkg/scenario"
)

// A fixture-driven stale-results experiment fires through the real forward
// proxy for a Qdrant Cloud cluster on its non-443 REST port: martian MITMs the
// CONNECT to :6333, the matcher sees the host with the port stripped, and the
// canned Query API body is served without dialing the upstream.
func TestE2E_StaleResultsQdrantCloudPort(t *testing.T) {
	s := &scenario.Scenario{
		Name: "test-rag-stale-results",
		Experiments: []scenario.Experiment{{
			Name:        "stale",
			Failure:     "stale-results",
			Probability: 1.0,
		}},
	}
	inj, env := startInjector(t, s)
	inj.Proxy().SetRoundTripper(&http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			t.Error("upstream must not be dialed when the fault fires")
			return nil, errors.New("no network in tests")
		},
	})
	client := clientThroughProxy(t, env)

	resp, err := client.Post("https://xyz.eu-central.aws.cloud.qdrant.io:6333/collections/docs/points/query",
		"application/json", strings.NewReader(`{"query":[0.1,0.2],"limit":2,"with_payload":true}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Faultkit-Synthetic"); got != "true" {
		t.Errorf("X-Faultkit-Synthetic = %q, want true", got)
	}
	if !strings.Contains(string(body), `"points":[{"id":"00000000-0000-4000-8000-00000000fa01"`) {
		t.Errorf("body is not the canned Qdrant stale-results body: %s", body)
	}

	select {
	case ev := <-inj.Events():
		if !ev.Fired || ev.Experiment != "stale-qdrant" || ev.Host != "xyz.eu-central.aws.cloud.qdrant.io" {
			t.Errorf("event = %+v, want fired stale-qdrant on the port-stripped host", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no fired event")
	}
}
