package proxy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// arrivals wraps an upstream handler and records how many requests reached
// it and when the first did.
type arrivals struct {
	next http.Handler

	mu      sync.Mutex
	hits    int
	firstAt time.Time
}

func (a *arrivals) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.hits++
	if a.hits == 1 {
		a.firstAt = time.Now()
	}
	a.mu.Unlock()
	a.next.ServeHTTP(w, r)
}

func (a *arrivals) seen() (int, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hits, a.firstAt
}

func delayScenario(fault faulttypes.Fault) *scenario.Scenario {
	return &scenario.Scenario{
		Name: "test-delay",
		Experiments: []scenario.Experiment{{
			Name:        "delay",
			Fault:       fault,
			Match:       scenario.Match{Host: "127.0.0.1"},
			Probability: 1.0,
		}},
	}
}

// Forward-proxy mode: the delay holds the request before anything else, and
// the request counts as one fired event whatever follows the delay.
func TestE2E_Delay(t *testing.T) {
	const (
		delay        = 50 * time.Millisecond
		upstreamBody = "real upstream body"
		cutAt        = 3
	)
	plain := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, upstreamBody)
	})
	short := openAIStream("", cutAt-1, true)
	cases := []struct {
		name           string
		fault          faulttypes.Fault
		upstream       http.Handler
		wantStatus     int
		wantBody       string // checked when set
		wantDataEvents int    // SSE events the client gets, checked when set
		wantUpstream   bool
	}{
		{
			name:         "delay only forwards after the delay",
			fault:        faulttypes.Fault{DelayMS: 50},
			upstream:     plain,
			wantStatus:   http.StatusOK,
			wantBody:     upstreamBody,
			wantUpstream: true,
		},
		{
			name:       "delay and status answers after the delay without the upstream",
			fault:      faulttypes.Fault{DelayMS: 50, HTTPStatus: 503},
			upstream:   plain,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "delay and a stream cut fire once",
			fault:          faulttypes.Fault{DelayMS: 50, StreamCutoffTokens: cutAt},
			upstream:       sseUpstream(openAIStream("", cutAt+5, true), false),
			wantStatus:     http.StatusOK,
			wantDataEvents: cutAt,
			wantUpstream:   true,
		},
		{
			name:         "delay and a stream shorter than the cut fire once",
			fault:        faulttypes.Fault{DelayMS: 50, StreamCutoffTokens: cutAt},
			upstream:     sseUpstream(short, false),
			wantStatus:   http.StatusOK,
			wantBody:     short,
			wantUpstream: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &arrivals{next: tc.upstream}
			inj, upstream, client := e2eInjector(t, delayScenario(tc.fault), up)

			start := time.Now()
			resp, err := client.Get(upstream.URL + "/v1/chat/completions")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("StatusCode = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantBody != "" && string(body) != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			if got := strings.Count(string(body), "data:"); tc.wantDataEvents != 0 && got != tc.wantDataEvents {
				t.Errorf("client got %d SSE events, want the %d before the cut", got, tc.wantDataEvents)
			}
			if got := time.Since(start); got < delay {
				t.Errorf("response after %v, want at least the %v delay", got, delay)
			}
			switch hits, at := up.seen(); {
			case !tc.wantUpstream && hits != 0:
				t.Errorf("upstream reached %d times, want 0", hits)
			case tc.wantUpstream && hits != 1:
				t.Errorf("upstream reached %d times, want 1", hits)
			case tc.wantUpstream && at.Sub(start) < delay:
				t.Errorf("upstream reached %v after the request, want at least the %v delay", at.Sub(start), delay)
			}

			// The delay's fired event is sent before the request goes on, so
			// after Stop the channel holds every event.
			if err := inj.Stop(context.Background()); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			assertCutoffEvents(t, inj.Events(), 1, 0)
		})
	}
}

// Stop during a long forward-proxy delay cuts it short: martian's Close
// would otherwise wait out the held connection. The client's connection is
// dropped and the request never reaches the upstream.
func TestE2E_StopCancelsDelay(t *testing.T) {
	up := &arrivals{next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	inj, upstream, client := e2eInjector(t, delayScenario(faulttypes.Fault{DelayMS: 60000}), up)

	errc := make(chan error, 1)
	go func() {
		resp, err := client.Get(upstream.URL + "/v1/chat/completions")
		if err == nil {
			_ = resp.Body.Close()
			err = fmt.Errorf("%w: %s", errGotResponse, resp.Status)
		}
		errc <- err
	}()

	// The fired event is sent when the delay starts.
	select {
	case ev := <-inj.Events():
		if !ev.Fired {
			t.Fatalf("first event = %+v, want the fired event of the delay starting", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delay never started")
	}

	start := time.Now()
	if err := inj.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := time.Since(start); got >= time.Second {
		t.Errorf("Stop took %v during a 60s delay, want < 1s", got)
	}
	select {
	case err := <-errc:
		if err == nil || errors.Is(err, errGotResponse) {
			t.Errorf("client err = %v, want the connection dropped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client still waiting after Stop")
	}
	// martian's Close waited for the held connection, so nothing can still
	// forward the request.
	if hits, _ := up.seen(); hits != 0 {
		t.Errorf("upstream reached %d times, want 0", hits)
	}
	assertCutoffEvents(t, inj.Events(), 0, 0)
}

// errGotResponse marks a client that got a response instead of an error.
var errGotResponse = errors.New("got a response")
