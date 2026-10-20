package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/martian/v3"

	"github.com/faultkit/faultkit/internal/inject"
	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// White-box: the delay draw, the base-URL handler's routing to a local
// upstream and Stop's cancellation are unexported.

// The jitter is a uniform draw from [DelayMS, DelayMS+DelayJitterMS],
// both ends included, and a seeded RNG repeats the same sequence.
func TestDelayForJitterRange(t *testing.T) {
	const draws = 2000
	fault := faulttypes.Fault{DelayMS: 100, DelayJitterMS: 20}
	lo := 100 * time.Millisecond
	hi := 120 * time.Millisecond

	f1 := NewFaulter(nil, nil, rand.New(rand.NewPCG(1, 2)))
	f2 := NewFaulter(nil, nil, rand.New(rand.NewPCG(1, 2)))
	var sawLo, sawHi bool
	for i := range draws {
		d := f1.delayFor(fault)
		if d < lo || d > hi {
			t.Fatalf("draw %d = %v, outside [%v, %v]", i, d, lo, hi)
		}
		sawLo = sawLo || d == lo
		sawHi = sawHi || d == hi
		if d2 := f2.delayFor(fault); d2 != d {
			t.Fatalf("draw %d: same seed gave %v and %v", i, d, d2)
		}
	}
	if !sawLo || !sawHi {
		t.Errorf("over %d draws saw lower bound %v, upper bound %v; want both", draws, sawLo, sawHi)
	}

	if d := f1.delayFor(faulttypes.Fault{DelayMS: 100}); d != lo {
		t.Errorf("without jitter: %v, want exactly %v", d, lo)
	}
}

func TestIsSynthetic(t *testing.T) {
	cases := []struct {
		name  string
		fault faulttypes.Fault
		want  bool
	}{
		{"status", faulttypes.Fault{HTTPStatus: 429}, true},
		{"headers", faulttypes.Fault{ResponseHeaders: map[string]string{"Retry-After": "30"}}, true},
		{"body", faulttypes.Fault{ResponseBody: "{}"}, true},
		{"delay and status", faulttypes.Fault{DelayMS: 50, HTTPStatus: 503}, true},
		{"delay only", faulttypes.Fault{DelayMS: 50, DelayJitterMS: 10}, false},
		{"stream cutoff", faulttypes.Fault{StreamCutoffTokens: 3}, false},
		{"delay and stream cutoff", faulttypes.Fault{DelayMS: 50, StreamCutoffTokens: 3}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSynthetic(c.fault); got != c.want {
				t.Errorf("isSynthetic = %v, want %v", got, c.want)
			}
		})
	}
}

// llm-slow-first-token fans out to every LLM provider as a valid delay-only
// fault, so each request is forwarded unchanged after the delay.
func TestExpandSlowFirstToken(t *testing.T) {
	s, err := scenario.LoadBuiltin("llm-slow-first-token")
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	exp, err := expandScenario(s, "")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	want := map[string]string{
		"api.openai.com":                  "/v1/chat/completions",
		"api.anthropic.com":               "/v1/messages",
		"bedrock-runtime.*.amazonaws.com": "/model/*",
	}
	got := map[string]string{}
	for _, e := range exp.Experiments {
		got[e.Match.Host] = e.Match.Path
		if !reflect.DeepEqual(e.Fault, faulttypes.Fault{DelayMS: 8000, DelayJitterMS: 7000}) {
			t.Errorf("host %s: fault = %+v, want delay 8000 + jitter 7000 only", e.Match.Host, e.Fault)
		}
		if err := e.Fault.Validate(); err != nil {
			t.Errorf("host %s: %v", e.Match.Host, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expanded host → path = %v, want %v", got, want)
	}
}

// upstreamRecorder serves delayTestUpstreamBody (or next, when set) and
// records how many requests arrived and when the first did.
type upstreamRecorder struct {
	next http.Handler

	mu      sync.Mutex
	hits    int
	firstAt time.Time
}

const delayTestUpstreamBody = "real upstream body"

func (u *upstreamRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.hits++
	if u.hits == 1 {
		u.firstAt = time.Now()
	}
	u.mu.Unlock()
	if u.next != nil {
		u.next.ServeHTTP(w, r)
		return
	}
	_, _ = io.WriteString(w, delayTestUpstreamBody)
}

// check verifies the upstream was reached exactly once, at least delay
// after start, or never.
func (u *upstreamRecorder) check(t *testing.T, start time.Time, want bool, delay time.Duration) {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case !want && u.hits != 0:
		t.Errorf("upstream reached %d times, want 0", u.hits)
	case want && u.hits != 1:
		t.Errorf("upstream reached %d times, want 1", u.hits)
	case want && u.firstAt.Sub(start) < delay:
		t.Errorf("upstream reached %v after the request, want at least the %v delay", u.firstAt.Sub(start), delay)
	}
}

// countEvents takes every event already in ch and counts the fired ones and
// the non-fired error ones.
func countEvents(t *testing.T, ch <-chan inject.Event) (fired, errs int) {
	t.Helper()
	for len(ch) > 0 {
		switch ev := <-ch; {
		case ev.Fired && ev.Err == "":
			fired++
		case !ev.Fired && ev.Err != "":
			errs++
		default:
			t.Errorf("unexpected event %+v", ev)
		}
	}
	return fired, errs
}

// Base-URL mode: the delay holds the request before anything else, and the
// request counts as one fired event whatever follows the delay.
func TestOriginDelay(t *testing.T) {
	const delay = 50 * time.Millisecond
	cases := []struct {
		name           string
		fault          faulttypes.Fault
		upstream       http.Handler // nil serves delayTestUpstreamBody
		wantStatus     int
		wantBody       string // checked when set
		wantDataEvents int    // SSE events the client gets, checked when set
		wantUpstream   bool
	}{
		{
			name:         "delay only forwards after the delay",
			fault:        faulttypes.Fault{DelayMS: 50},
			wantStatus:   http.StatusOK,
			wantBody:     delayTestUpstreamBody,
			wantUpstream: true,
		},
		{
			name:       "delay and status answers after the delay without the upstream",
			fault:      faulttypes.Fault{DelayMS: 50, HTTPStatus: 503},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "delay and a stream cut fire once",
			fault:          faulttypes.Fault{DelayMS: 50, StreamCutoffTokens: 3},
			upstream:       originSSEUpstream(openAIDeltas(5), false),
			wantStatus:     http.StatusOK,
			wantDataEvents: 3,
			wantUpstream:   true,
		},
		{
			name:         "delay and a stream shorter than the cut fire once",
			fault:        faulttypes.Fault{DelayMS: 50, StreamCutoffTokens: 3},
			upstream:     originSSEUpstream(openAIDeltas(2), false),
			wantStatus:   http.StatusOK,
			wantBody:     openAIDeltas(2),
			wantUpstream: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, events := newOriginTestHandler(t, sealTestScenario(c.fault))
			up := &upstreamRecorder{next: c.upstream}
			routeToLocalUpstream(t, h, up)

			start := time.Now()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions", nil))

			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			if c.wantBody != "" && rec.Body.String() != c.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), c.wantBody)
			}
			if got := strings.Count(rec.Body.String(), "data:"); c.wantDataEvents != 0 && got != c.wantDataEvents {
				t.Errorf("client got %d SSE events, want the %d before the cut", got, c.wantDataEvents)
			}
			if got := time.Since(start); got < delay {
				t.Errorf("response after %v, want at least the %v delay", got, delay)
			}
			up.check(t, start, c.wantUpstream, delay)
			if fired, errs := countEvents(t, events); fired != 1 || errs != 0 {
				t.Errorf("fired events = %d, error events = %d; want 1 and 0", fired, errs)
			}
		})
	}
}

// serveRecordingExit serves h on a local server and reports, per request,
// what the handler panicked with (nil if it returned). net/http still sees
// the panic, so http.ErrAbortHandler drops the connection as usual.
func serveRecordingExit(t *testing.T, h http.Handler) (*httptest.Server, <-chan any) {
	t.Helper()
	exits := make(chan any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			exits <- p
			if p != nil {
				panic(p)
			}
		}()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, exits
}

// errGotResponse is what getAsync delivers when the client got a response
// instead of an error.
var errGotResponse = errors.New("got a response")

// getAsync sends a GET for url with ctx and delivers the client's error,
// or errGotResponse if a response arrived.
func getAsync(ctx context.Context, url string) <-chan error {
	errc := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			var resp *http.Response
			resp, err = http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				err = fmt.Errorf("%w: %s", errGotResponse, resp.Status)
			}
		}
		errc <- err
	}()
	return errc
}

// awaitClientErr waits for the outcome getAsync delivers.
func awaitClientErr(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("client still waiting")
		return nil
	}
}

// awaitFired waits for the fired event a delay sends when it starts.
func awaitFired(t *testing.T, ch <-chan inject.Event) {
	t.Helper()
	select {
	case ev := <-ch:
		if !ev.Fired {
			t.Fatalf("first event = %+v, want the fired event of the delay starting", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delay never started")
	}
}

// awaitAbort waits for the base-URL handler to drop the request.
func awaitAbort(t *testing.T, exits <-chan any) {
	t.Helper()
	select {
	case p := <-exits:
		if p != http.ErrAbortHandler {
			t.Errorf("handler ended with %v, want it to drop the connection (http.ErrAbortHandler)", p)
		}
	case <-time.After(time.Second):
		t.Fatal("handler still holding the request 1s after the delay was cancelled")
	}
}

// Stop during a long base-URL delay cuts it short: Stop returns at once, the
// handler drops the connection, and the request never reaches the upstream.
func TestOriginStopCancelsDelay(t *testing.T) {
	inj := startSealTestInjector(t, sealTestScenario(faulttypes.Fault{DelayMS: 60000}), true)
	h, ok := inj.originSrv.Handler.(*originHandler)
	if !ok {
		t.Fatalf("origin handler is %T, want *originHandler", inj.originSrv.Handler)
	}
	up := &upstreamRecorder{}
	routeToLocalUpstream(t, h, up)
	srv, exits := serveRecordingExit(t, h)

	errc := getAsync(context.Background(), srv.URL+"/__fk/openai/v1/models")
	awaitFired(t, inj.Events())

	start := time.Now()
	if err := inj.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := time.Since(start); got >= time.Second {
		t.Errorf("Stop took %v during a 60s delay, want < 1s", got)
	}
	awaitAbort(t, exits)
	if err := awaitClientErr(t, errc); err == nil || errors.Is(err, errGotResponse) {
		t.Errorf("client err = %v, want the connection dropped", err)
	}
	up.check(t, start, false, 0)
	if got := drainEvents(t, inj.Events()); len(got) != 0 {
		t.Errorf("events after the delay started = %+v, want none", got)
	}
}

// A base-URL client that gives up during the delay of a request without a
// body ends the delay: the handler drops the request instead of forwarding
// it when the delay would have run out.
func TestOriginClientCancelEndsDelay(t *testing.T) {
	h, events := newOriginTestHandler(t, sealTestScenario(faulttypes.Fault{DelayMS: 60000}))
	up := &upstreamRecorder{}
	routeToLocalUpstream(t, h, up)
	srv, exits := serveRecordingExit(t, h)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := getAsync(ctx, srv.URL+"/__fk/openai/v1/models")
	awaitFired(t, events)

	cancel()
	if err := awaitClientErr(t, errc); !errors.Is(err, context.Canceled) {
		t.Errorf("client err = %v, want context.Canceled", err)
	}
	awaitAbort(t, exits)
	up.check(t, time.Time{}, false, 0)
	if fired, errs := countEvents(t, events); fired != 0 || errs != 0 {
		t.Errorf("events after the delay started: %d fired, %d errors; want none", fired, errs)
	}
}

// A request that reaches its delay after cancelDelays already ran (one a
// leftover process sends while Stop runs) gets no delay. It is dropped,
// never forwarded, and doesn't count as fired, so it can't turn a run where
// no fault applied into a fired verdict.
func TestDelayAlreadyCancelledNotFired(t *testing.T) {
	t.Run("forward-proxy", func(t *testing.T) {
		events := make(chan inject.Event, 8)
		f := NewFaulter(sealTestScenario(faulttypes.Fault{DelayMS: 60000}), events, nil)
		f.cancelDelays()

		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		req := newSealTestRequest()
		ctx, remove, err := martian.TestContext(req, server, nil)
		if err != nil {
			t.Fatalf("martian.TestContext: %v", err)
		}
		t.Cleanup(remove)

		start := time.Now()
		if err := f.ModifyRequest(req); err != nil {
			t.Fatalf("ModifyRequest: %v", err)
		}
		if got := time.Since(start); got >= time.Second {
			t.Errorf("ModifyRequest held the request %v after cancelDelays", got)
		}
		// martian forwards nothing for a hijacked session.
		if !ctx.Session().Hijacked() {
			t.Error("connection not dropped: martian would forward the request")
		}
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Errorf("client read err = %v, want io.EOF from the closed connection", err)
		}
		if fired, errs := countEvents(t, events); fired != 0 || errs != 0 {
			t.Errorf("events = %d fired, %d errors; want none", fired, errs)
		}
	})

	t.Run("base-url", func(t *testing.T) {
		h, events := newOriginTestHandler(t, sealTestScenario(faulttypes.Fault{DelayMS: 60000}))
		up := &upstreamRecorder{}
		routeToLocalUpstream(t, h, up)
		srv, exits := serveRecordingExit(t, h)
		h.faulter.cancelDelays()

		errc := getAsync(context.Background(), srv.URL+"/__fk/openai/v1/models")
		awaitAbort(t, exits)
		if err := awaitClientErr(t, errc); err == nil || errors.Is(err, errGotResponse) {
			t.Errorf("client err = %v, want the connection dropped", err)
		}
		up.check(t, time.Time{}, false, 0)
		if fired, errs := countEvents(t, events); fired != 0 || errs != 0 {
			t.Errorf("events = %d fired, %d errors; want none", fired, errs)
		}
	})
}
