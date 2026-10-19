package proxy

import (
	"net/http"
	"time"

	"github.com/google/martian/v3"

	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// delayFor draws how long fault holds a request: DelayMS plus a uniform
// jitter in [0, DelayJitterMS] from the faulter's RNG.
func (f *Faulter) delayFor(fault faulttypes.Fault) time.Duration {
	ms := fault.DelayMS
	if fault.DelayJitterMS > 0 {
		f.mu.Lock()
		ms += f.rng.IntN(fault.DelayJitterMS + 1)
		f.mu.Unlock()
	}
	return time.Duration(ms) * time.Millisecond
}

// hold applies exp's delay to req before anything else happens to it. The
// delay starting is the fault's first applied effect, so hold emits the
// request's one fired event before it waits; emitEffect keeps the effects
// after it from emitting again.
//
// hold reports false if the wait was cut short, by cancelDelays (Stop) or
// by done (the client went away). The caller must then drop the client
// connection and never forward the request. A request that arrives already
// cancelled gets no delay at all, so it doesn't count as fired.
func (f *Faulter) hold(req *http.Request, exp *scenario.Experiment, host string, done <-chan struct{}) bool {
	select {
	case <-f.stop:
		return false
	case <-done:
		return false
	default:
	}
	f.emit(req, exp, host)
	t := time.NewTimer(f.delayFor(exp.Fault))
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-f.stop:
		return false
	case <-done:
		return false
	}
}

// cancelDelays cuts every pending and later delay short. Injector.Stop
// calls it before it shuts the transports down, because martian's Close
// waits for every connection, including one held in a delay.
func (f *Faulter) cancelDelays() {
	f.stopOnce.Do(func() { close(f.stop) })
}

// emitEffect emits the fired event for an effect that follows the delay:
// a synthetic response or a stream cut. When the fault has a delay, hold
// already emitted the request's fired event at the delay's start, so the
// request is not counted twice. With a stream cutoff, the delay alone
// therefore counts as the applied fault, even if the stream never reaches
// the cut point.
func (f *Faulter) emitEffect(req *http.Request, exp *scenario.Experiment, host string) {
	if exp.Fault.DelayMS > 0 {
		return
	}
	f.emit(req, exp, host)
}

// dropConn closes the client connection behind ctx. martian sees the
// hijacked session, so it neither round-trips the request nor writes a
// response.
func dropConn(ctx *martian.Context) {
	conn, _, err := ctx.Session().Hijack()
	// Hijack fails only for a session that was already hijacked, whose
	// connection is no longer ours to close.
	if err != nil || conn == nil {
		return
	}
	_ = conn.Close()
}
