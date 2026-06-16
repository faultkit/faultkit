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
	"github.com/faultkit/faultkit/pkg/scenario"
)

// syntheticHeader marks responses that faultkit synthesized rather
// than forwarded from upstream. Production agent code should not read
// this; it exists so leaks are visible in logs.
const syntheticHeader = "X-Faultkit-Synthetic"

const ctxKeyExperiment = "faultkit.experiment"

// Faulter is martian's request and response modifier for the proxy
// injector. It matches against the scenario, rolls the dice, and
// either skips the upstream round trip (synthetic responses) or wraps
// the upstream response (streaming-cutoff).
type Faulter struct {
	matcher *Matcher
	events  chan<- inject.Event

	mu  sync.Mutex
	rng *rand.Rand

	// seen counts target requests that reached faultkit (matched or not),
	// so the CLI can warn when zero traffic arrived. See observe/Seen.
	seen atomic.Int64
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
	}
}

// Decide returns the experiment that should fire for req, or nil if
// no experiment matches or the dice roll declined. Pure function;
// useful for unit tests that can't construct a martian context.
func (f *Faulter) Decide(req *http.Request) *scenario.Experiment {
	return f.roll(f.matcher.Match(req))
}

// DecideHostPath is Decide for callers that resolve the host and path
// themselves (base-URL/origin mode) rather than reading them from a
// request. Same matching and dice roll.
func (f *Faulter) DecideHostPath(host, path string) *scenario.Experiment {
	return f.roll(f.matcher.matchHostPath(host, path))
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
	// Stream-cutoff faults need the real upstream response; everything
	// else is fully synthetic so we can short-circuit the round trip.
	if exp.Fault.StreamCutoffTokens == 0 {
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
		wrapStreamCutoff(res, exp.Fault.StreamCutoffTokens, func(err error) {
			f.emitErr(res.Request, exp, host, err)
		})
		f.emit(res.Request, exp, host)
		return nil
	}

	syn := fixtures.Build(host, exp.Fault)
	applySynthetic(res, syn)
	f.emit(res.Request, exp, host)
	return nil
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
// emits the fired event. It is the http.ResponseWriter analogue of
// applySynthetic (which mutates an *http.Response on the forward-proxy
// path), so base-URL/origin mode renders synthetic faults identically
// without re-implementing the shape.
func (f *Faulter) writeSyntheticResponse(w http.ResponseWriter, req *http.Request, exp *scenario.Experiment, host string) {
	syn := fixtures.Build(host, exp.Fault)
	for k, v := range syn.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set(syntheticHeader, "true")
	w.WriteHeader(syn.Status)
	_, _ = w.Write(syn.Body)
	f.emit(req, exp, host)
}

func (f *Faulter) emit(req *http.Request, exp *scenario.Experiment, host string) {
	inject.TrySend(f.events, buildEvent(req, exp, host, ""))
}

// emitErr publishes a non-fatal error encountered while applying a
// fault (e.g. upstream stream read error during stream-cutoff). Fired
// stays true because the fault behavior was applied; the Err field
// surfaces the readout failure separately.
func (f *Faulter) emitErr(req *http.Request, exp *scenario.Experiment, host string, err error) {
	if err == nil {
		return
	}
	inject.TrySend(f.events, buildEvent(req, exp, host, err.Error()))
}

func buildEvent(req *http.Request, exp *scenario.Experiment, host, errMsg string) inject.Event {
	path := ""
	if req != nil && req.URL != nil {
		path = req.URL.Path
	}
	return inject.Event{
		Experiment: exp.Name,
		Fault:      exp.Fault,
		Fired:      true,
		Host:       host,
		Path:       path,
		Timestamp:  time.Now(),
		Err:        errMsg,
	}
}
