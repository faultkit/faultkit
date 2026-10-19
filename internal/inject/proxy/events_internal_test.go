package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/martian/v3"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// White-box: the injector's faulter, origin handler and event seal are
// unexported. These tests pin that an emit racing or outliving
// Injector.Stop is dropped. A send on the closed Events channel would
// panic, exit 2 and lose the report.

const sealTestHost = "api.openai.com"

// errReadOnClosedBody mirrors net/http's error for a response body closed
// mid-read: what the stream-cutoff goroutine sees once the client is gone.
var errReadOnClosedBody = errors.New("http: read on closed response body")

func sealTestScenario(fault faulttypes.Fault) *scenario.Scenario {
	return &scenario.Scenario{
		Name: "seal-test",
		Experiments: []scenario.Experiment{{
			Name:        "openai-fault",
			Fault:       fault,
			Match:       scenario.Match{Host: sealTestHost, Path: "/v1/*"},
			Probability: 1,
		}},
	}
}

// startSealTestInjector starts an injector for s, in base-URL mode when
// baseURL is set. The cleanup Stop also checks that a repeated Stop stays a
// no-op after the test's own Stop.
func startSealTestInjector(t *testing.T, s *scenario.Scenario, baseURL bool) *Injector {
	t.Helper()
	inj := New()
	inj.UseBaseURL(baseURL)
	if _, err := inj.Start(context.Background(), s); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := inj.Stop(context.Background()); err != nil {
			t.Errorf("repeated Stop: %v", err)
		}
	})
	return inj
}

func newSealTestRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "https://"+sealTestHost+"/v1/chat/completions", nil)
}

func sealTestExperiment() *scenario.Experiment {
	return &scenario.Experiment{Name: "late", Fault: faulttypes.Fault{HTTPStatus: 429}}
}

// drainEvents reads ch until it is closed. Stop must close it, so a channel
// that stays open fails the test.
func drainEvents(t *testing.T, ch <-chan inject.Event) []inject.Event {
	t.Helper()
	var got []inject.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-timeout:
			t.Fatal("Events channel still open after Stop")
			return nil
		}
	}
}

// closeEvents seals on its own, independent of Stop's once: it closes the
// channel exactly once, and emit and emitErr after it are dropped.
func TestCloseEventsSealsOnce(t *testing.T) {
	events := make(chan inject.Event, 1)
	f := NewFaulter(&scenario.Scenario{Name: "empty"}, events, nil)
	req, exp := newSealTestRequest(), sealTestExperiment()

	f.closeEvents()
	f.closeEvents()
	f.emit(req, exp, sealTestHost)
	f.emitErr(req, exp, sealTestHost, errReadOnClosedBody)

	if got := drainEvents(t, events); len(got) != 0 {
		t.Errorf("events = %+v, want none delivered after closeEvents", got)
	}
}

// An emit before Stop is delivered. emit and emitErr after Stop are dropped
// without panicking, and a second Stop is still a no-op.
func TestEmitAfterStopIsDropped(t *testing.T) {
	inj := startSealTestInjector(t, sealTestScenario(faulttypes.Fault{HTTPStatus: 429}), false)
	f := inj.faulter
	req, exp := newSealTestRequest(), sealTestExperiment()

	f.emit(req, exp, sealTestHost)
	if err := inj.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	f.emit(req, exp, sealTestHost)
	f.emitErr(req, exp, sealTestHost, errReadOnClosedBody)
	if err := inj.Stop(context.Background()); err != nil {
		t.Errorf("second Stop: %v", err)
	}

	got := drainEvents(t, inj.Events())
	if len(got) != 1 || got[0].Err != "" {
		t.Errorf("events = %+v, want only the fired event emitted before Stop", got)
	}
}

// Path B: http.Server.Close does not wait for base-URL handlers, so one can
// render a synthetic fault after Stop. The response is still written; only
// the event is dropped.
func TestOriginSyntheticFaultAfterStop(t *testing.T) {
	inj := startSealTestInjector(t, sealTestScenario(faulttypes.Fault{HTTPStatus: 429}), true)
	h := inj.originSrv.Handler
	if err := inj.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if got := drainEvents(t, inj.Events()); len(got) != 0 {
		t.Errorf("events = %+v, want none delivered after Stop", got)
	}
}

// closedMidReadUpstream is an idle upstream SSE body. Read blocks until the
// first Close, then fails like a real body closed mid-read. The cutoff
// goroutine's own deferred Close (the second call) runs only after onErr has
// returned, so done marks the late emitErr as finished.
type closedMidReadUpstream struct {
	unblock chan struct{}
	done    chan struct{}
	closes  atomic.Int32
}

func newClosedMidReadUpstream() *closedMidReadUpstream {
	return &closedMidReadUpstream{unblock: make(chan struct{}), done: make(chan struct{})}
}

func (u *closedMidReadUpstream) Read([]byte) (int, error) {
	<-u.unblock
	return 0, errReadOnClosedBody
}

func (u *closedMidReadUpstream) Close() error {
	switch u.closes.Add(1) {
	case 1:
		close(u.unblock)
	case 2:
		close(u.done)
	}
	return nil
}

// wrapViaFaulter installs the stream cutoff through the forward-proxy path:
// martian's request and response modifiers, with a test martian context
// standing in for the proxied connection.
func wrapViaFaulter(t *testing.T, inj *Injector, res *http.Response) {
	t.Helper()
	_, remove, err := martian.TestContext(res.Request, nil, nil)
	if err != nil {
		t.Fatalf("martian.TestContext: %v", err)
	}
	t.Cleanup(remove)
	if err := inj.faulter.ModifyRequest(res.Request); err != nil {
		t.Fatalf("ModifyRequest: %v", err)
	}
	if err := inj.faulter.ModifyResponse(res); err != nil {
		t.Fatalf("ModifyResponse: %v", err)
	}
}

// wrapViaOrigin installs the stream cutoff through the base-URL path: the
// reverse proxy's ModifyResponse, with the request context ServeHTTP sets.
func wrapViaOrigin(t *testing.T, inj *Injector, res *http.Response) {
	t.Helper()
	h, ok := inj.originSrv.Handler.(*originHandler)
	if !ok {
		t.Fatalf("origin handler is %T, want *originHandler", inj.originSrv.Handler)
	}
	p, rest, ok := providerForPath("/__fk/openai/v1/chat/completions")
	if !ok {
		t.Fatal("openai base-URL prefix not routed")
	}
	exp := inj.faulter.DecideHostPath(p.upstream, rest)
	if exp == nil {
		t.Fatal("stream-cutoff experiment did not fire")
	}
	ctx := context.WithValue(res.Request.Context(), ctxOriginProvider, p)
	ctx = context.WithValue(ctx, ctxOriginExp, exp)
	res.Request = res.Request.WithContext(ctx)
	if err := h.rp.ModifyResponse(res); err != nil {
		t.Fatalf("ModifyResponse: %v", err)
	}
}

// Path A: Stop does not join the stream-cutoff goroutine. When the client
// goes away, closing the wrapped body closes the upstream, and the
// goroutine's failed read reaches emitErr after the seal. It must be dropped.
func TestStreamCutoffUpstreamClosedAfterStop(t *testing.T) {
	cases := []struct {
		name    string
		baseURL bool
		wrap    func(*testing.T, *Injector, *http.Response)
	}{
		{name: "forward-proxy", wrap: wrapViaFaulter},
		{name: "base-url", baseURL: true, wrap: wrapViaOrigin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A cut point the stream never reaches: only the client leaving ends it.
			inj := startSealTestInjector(t, sealTestScenario(faulttypes.Fault{StreamCutoffTokens: 1000}), tc.baseURL)
			up := newClosedMidReadUpstream()
			res := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{sseContentType}},
				Body:       up,
				Request:    newSealTestRequest(),
			}
			tc.wrap(t, inj, res)
			if _, ok := res.Body.(*streamBody); !ok {
				t.Fatal("response body not wrapped for stream cutoff")
			}

			if err := inj.Stop(context.Background()); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			_ = res.Body.Close() // the client goes away after Stop
			select {
			case <-up.done:
			case <-time.After(5 * time.Second):
				t.Fatal("stream-cutoff goroutine did not finish after the upstream closed")
			}

			// The stream was never cut, so nothing fired before Stop, and the
			// late error event is dropped.
			if got := drainEvents(t, inj.Events()); len(got) != 0 {
				t.Errorf("events = %+v, want none", got)
			}
		})
	}
}

// Non-fired diagnostics may fill at most half the event buffer, so a real
// cut after a flood of them is still recorded and the run counts as fired.
func TestDiagnosticsLeaveRoomForFiredEvent(t *testing.T) {
	cases := []struct {
		name    string
		baseURL bool
		wrap    func(*testing.T, *Injector, *http.Response)
	}{
		{name: "forward-proxy", wrap: wrapViaFaulter},
		{name: "base-url", baseURL: true, wrap: wrapViaOrigin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const cutAt = 3
			inj := startSealTestInjector(t, sealTestScenario(faulttypes.Fault{StreamCutoffTokens: cutAt}), tc.baseURL)
			req, exp := newSealTestRequest(), sealTestExperiment()
			for range eventBuffer + 10 {
				inj.faulter.emitErr(req, exp, sealTestHost, errReadOnClosedBody)
			}

			res := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{sseContentType}},
				Body:       io.NopCloser(strings.NewReader(openAIDeltas(cutAt))),
				Request:    newSealTestRequest(),
			}
			tc.wrap(t, inj, res)
			if _, err := io.ReadAll(res.Body); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if err := inj.Stop(context.Background()); err != nil {
				t.Fatalf("Stop: %v", err)
			}

			var fired, diagnostics int
			for _, ev := range drainEvents(t, inj.Events()) {
				if ev.Fired {
					fired++
				} else {
					diagnostics++
				}
			}
			if fired != 1 {
				t.Errorf("fired events = %d, want the cut's 1 despite the diagnostics", fired)
			}
			if diagnostics != eventBuffer/2 {
				t.Errorf("diagnostics kept = %d, want half the buffer (%d)", diagnostics, eventBuffer/2)
			}
		})
	}
}

// An injector whose Start never built a faulter still closes its channel
// once, however often Stop runs.
func TestStopWithoutFaulterClosesEventsOnce(t *testing.T) {
	inj := New()
	for i := range 2 {
		if err := inj.Stop(context.Background()); err != nil {
			t.Fatalf("Stop #%d: %v", i+1, err)
		}
	}
	if got := drainEvents(t, inj.Events()); len(got) != 0 {
		t.Errorf("events = %+v, want none", got)
	}
}

// Emitters racing Stop; meaningful under -race. Each emit lands before the
// seal or is dropped. None panics or races the channel close.
func TestEmitConcurrentWithStop(t *testing.T) {
	inj := startSealTestInjector(t, sealTestScenario(faulttypes.Fault{HTTPStatus: 429}), false)
	f := inj.faulter
	exp := sealTestExperiment()

	received := make(chan struct{})
	drained := make(chan int, 1)
	go func() {
		n := 0
		for range inj.Events() {
			if n == 0 {
				close(received)
			}
			n++
		}
		drained <- n
	}()

	const emitters, lateEmits = 8, 100
	stopped := make(chan struct{})
	var wg sync.WaitGroup
	for range emitters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := newSealTestRequest()
			for {
				select {
				case <-stopped:
					for range lateEmits {
						f.emit(req, exp, sealTestHost)
						f.emitErr(req, exp, sealTestHost, errReadOnClosedBody)
					}
					return
				default:
					f.emit(req, exp, sealTestHost)
					f.emitErr(req, exp, sealTestHost, errReadOnClosedBody)
				}
			}
		}()
	}

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no event delivered before Stop")
	}
	if err := inj.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
	close(stopped)
	wg.Wait()

	if n := <-drained; n == 0 {
		t.Error("drained no events, want the ones emitted before Stop")
	}
}
