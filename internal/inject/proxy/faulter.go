package proxy

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/google/martian/v3"

	"github.com/faultkit-dev/faultkit/internal/inject"
	"github.com/faultkit-dev/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
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
}

// NewFaulter constructs a Faulter for s. If rng is nil, a
// time-seeded source is used.
func NewFaulter(s *scenario.Scenario, events chan<- inject.Event, rng *rand.Rand) *Faulter {
	if rng == nil {
		// #nosec G404 -- math/rand is correct for fault-probability dice rolls; this isn't a security boundary.
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
	exp := f.matcher.Match(req)
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

// emit publishes a fired-fault event. Drops silently when the buffer
// is full so the proxy hot path never blocks; consumers must drain.
func (f *Faulter) emit(req *http.Request, exp *scenario.Experiment, host string) {
	f.send(buildEvent(req, exp, host, true, ""))
}

// emitErr publishes a non-fatal error encountered while applying a
// fault (e.g. upstream stream read error during stream-cutoff). Fired
// stays true because the fault behavior was applied even when readout
// later failed; the Err field surfaces the failure to the user.
func (f *Faulter) emitErr(req *http.Request, exp *scenario.Experiment, host string, err error) {
	if err == nil {
		return
	}
	f.send(buildEvent(req, exp, host, true, err.Error()))
}

func (f *Faulter) send(ev inject.Event) {
	if f.events == nil {
		return
	}
	select {
	case f.events <- ev:
	default:
	}
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
