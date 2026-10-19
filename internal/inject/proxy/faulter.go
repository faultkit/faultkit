package proxy

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/martian/v3"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// syntheticHeader marks responses that faultkit synthesized rather
// than forwarded from upstream. Production agent code should not read
// this; it exists so leaks are visible in logs.
const syntheticHeader = "X-Faultkit-Synthetic"

const ctxKeyExperiment = "faultkit.experiment"

// Faulter is martian's request and response modifier for the proxy
// injector. It matches against the scenario, rolls the dice, holds the
// request for the fault's delay if it has one, and then either skips the
// upstream round trip (synthetic responses), wraps the upstream response
// (streaming-cutoff), or forwards it unchanged (delay only).
type Faulter struct {
	matcher *Matcher

	// sendMu guards sends on events, the close of events, and sealed.
	// Emits send only while unsealed and closeEvents seals and closes
	// under the same lock, so an emit that outlives Injector.Stop (a
	// stream-cutoff goroutine, a base-URL handler still running after
	// http.Server.Close) is dropped instead of panicking on the closed
	// channel.
	sendMu sync.Mutex
	sealed bool
	events chan<- inject.Event

	mu  sync.Mutex
	rng *rand.Rand

	// seen counts target requests that reached faultkit (matched or not),
	// so the CLI can warn when zero traffic arrived. See observe/Seen.
	seen atomic.Int64

	// stop is closed once, by cancelDelays, to cut every delay short.
	stop     chan struct{}
	stopOnce sync.Once
}

// observe records that one target request reached faultkit.
func (f *Faulter) observe() { f.seen.Add(1) }

// Seen reports how many target requests reached faultkit.
func (f *Faulter) Seen() int { return int(f.seen.Load()) }

// NewFaulter constructs a Faulter for s. If rng is nil, a
// time-seeded source is used.
func NewFaulter(s *scenario.Scenario, events chan<- inject.Event, rng *rand.Rand) *Faulter {
	if rng == nil {
		// #nosec G404,G115 -- math/rand is correct for fault-probability dice rolls; this isn't a security boundary. UnixNano is positive for ~292 years past 1970, so the uint64 cast is safe.
		rng = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	}
	return &Faulter{
		matcher: NewMatcher(s),
		events:  events,
		rng:     rng,
		stop:    make(chan struct{}),
	}
}

// Decide returns the experiment that should fire for req, or nil if
// no experiment matches or the dice roll declined. Pure function;
// useful for unit tests that can't construct a martian context.
func (f *Faulter) Decide(req *http.Request) *scenario.Experiment {
	return f.roll(f.matcher.Match(req))
}

// DecideHostPath is Decide for callers that resolve the method, host and
// path themselves (base-URL/origin mode) rather than reading them from a
// request. Same matching and dice roll.
func (f *Faulter) DecideHostPath(method, host, path string) *scenario.Experiment {
	return f.roll(f.matcher.matchHostPath(method, host, path))
}

// roll applies the probability dice to a matched experiment, returning it
// if the fault should fire, or nil otherwise (including when exp is nil).
func (f *Faulter) roll(exp *scenario.Experiment) *scenario.Experiment {
	if exp == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rng.Float64() < exp.Probability {
		return exp
	}
	return nil
}

// ModifyRequest implements martian.RequestModifier.
func (f *Faulter) ModifyRequest(req *http.Request) error {
	// Count real requests (the CONNECT that establishes the MITM tunnel is
	// not one) so the CLI can tell "no traffic reached us" from "matched
	// nothing".
	if req.Method != http.MethodConnect {
		f.observe()
	}
	exp := f.Decide(req)
	if exp == nil {
		return nil
	}

	ctx := martian.NewContext(req)
	if ctx == nil {
		return nil
	}
	ctx.Set(ctxKeyExperiment, exp)
	// martian doesn't cancel a request when its client goes away, so only
	// Stop can cut the delay short here.
	if exp.Fault.DelayMS > 0 && !f.hold(req, exp, normalizeHost(hostFromRequest(req)), nil) {
		dropConn(ctx)
		return nil
	}
	// Synthetic faults short-circuit the round trip. A stream cutoff needs
	// the real upstream response, and a delay-only fault forwards the
	// request unchanged.
	if isSynthetic(exp.Fault) {
		ctx.SkipRoundTrip()
	}
	return nil
}

// ModifyResponse implements martian.ResponseModifier.
func (f *Faulter) ModifyResponse(res *http.Response) error {
	if res.Request == nil {
		return nil
	}
	ctx := martian.NewContext(res.Request)
	if ctx == nil {
		return nil
	}
	raw, ok := ctx.Get(ctxKeyExperiment)
	if !ok {
		return nil
	}
	exp, ok := raw.(*scenario.Experiment)
	if !ok {
		return nil
	}

	host := normalizeHost(hostFromRequest(res.Request))

	if exp.Fault.StreamCutoffTokens > 0 {
		// The cut happens later, on the stream's forwarding goroutine.
		// Without a delay, the fault fires only if the stream reaches the
		// cut point.
		wrapStreamCutoff(res, exp.Fault.StreamCutoffTokens, func() {
			f.emitEffect(res.Request, exp, host)
		}, func(err error) {
			f.emitErr(res.Request, exp, host, err)
		})
		return nil
	}
	if !isSynthetic(exp.Fault) {
		// Delay only: the delay was the whole fault, and the upstream
		// response passes through.
		return nil
	}

	syn := fixtures.Build(host, exp.Fault)
	applySynthetic(res, syn)
	f.emitEffect(res.Request, exp, host)
	return nil
}

// isSynthetic reports whether faultkit answers a request for f itself,
// without the upstream round trip: f sets a status, body or headers and no
// stream cutoff. A delay-only fault is forwarded after its delay, and a
// stream cutoff needs the real upstream stream.
func isSynthetic(f faulttypes.Fault) bool {
	return f.StreamCutoffTokens == 0 &&
		(f.HTTPStatus != 0 || len(f.ResponseHeaders) > 0 || f.ResponseBody != "")
}

func applySynthetic(res *http.Response, syn fixtures.Synthetic) {
	if res.Body != nil {
		_ = res.Body.Close()
	}
	res.StatusCode = syn.Status
	res.Status = fmt.Sprintf("%d %s", syn.Status, http.StatusText(syn.Status))
	res.Body = io.NopCloser(bytes.NewReader(syn.Body))
	res.ContentLength = int64(len(syn.Body))

	res.Header = make(http.Header, len(syn.Headers)+1)
	for k, v := range syn.Headers {
		res.Header.Set(k, v)
	}
	res.Header.Set(syntheticHeader, "true")
}

// writeSyntheticResponse renders a fully synthetic fault for exp onto w and
// emits the fired event, unless the fault's delay already did. It is the
// http.ResponseWriter analogue of applySynthetic (which mutates an
// *http.Response on the forward-proxy path), so base-URL/origin mode
// renders synthetic faults identically without re-implementing the shape.
func (f *Faulter) writeSyntheticResponse(w http.ResponseWriter, req *http.Request, exp *scenario.Experiment, host string) {
	syn := fixtures.Build(host, exp.Fault)
	for k, v := range syn.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set(syntheticHeader, "true")
	w.WriteHeader(syn.Status)
	_, _ = w.Write(syn.Body)
	f.emitEffect(req, exp, host)
}

func (f *Faulter) emit(req *http.Request, exp *scenario.Experiment, host string) {
	f.send(buildEvent(req, exp, host, true, ""))
}

// emitErr publishes an error that kept a fault from being applied (e.g. an
// upstream stream read error before the stream-cutoff point). Fired is
// false, so the event does not count as a fired fault; Err carries the
// reason, like the eBPF injector's ringbuf error event.
func (f *Faulter) emitErr(req *http.Request, exp *scenario.Experiment, host string, err error) {
	if err == nil {
		return
	}
	f.send(buildEvent(req, exp, host, false, err.Error()))
}

// send publishes ev unless closeEvents has sealed the channel. TrySend never
// blocks, so holding sendMu across it cannot deadlock.
//
// A non-fired diagnostic is dropped once the buffer is half full. Those
// events don't change the verdict, but a fired one does, so diagnostics
// must not fill the slots a fired event needs. Every send holds sendMu, so
// between the check and the send the buffer can only drain: diagnostics
// never hold more than half of it.
func (f *Faulter) send(ev inject.Event) {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	if f.sealed {
		return
	}
	if !ev.Fired && len(f.events) >= cap(f.events)/2 {
		return
	}
	inject.TrySend(f.events, ev)
}

// closeEvents seals the faulter and closes its event channel exactly once;
// later emits are dropped. It must be the channel's only closer.
func (f *Faulter) closeEvents() {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	if f.sealed {
		return
	}
	f.sealed = true
	close(f.events)
}

func buildEvent(req *http.Request, exp *scenario.Experiment, host string, fired bool, errMsg string) inject.Event {
	path := ""
	if req != nil && req.URL != nil {
		path = req.URL.Path
	}
	return inject.Event{
		Experiment: exp.Name,
		Fault:      exp.Fault,
		Fired:      fired,
		Host:       host,
		Path:       path,
		Timestamp:  time.Now(),
		Err:        errMsg,
	}
}
