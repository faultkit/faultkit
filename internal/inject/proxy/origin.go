package proxy

import (
	"context"
	"net/http"
	"net/http/httputil"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/scenario"
)

type originCtxKey int

const (
	ctxOriginProvider originCtxKey = iota
	ctxOriginExp
)

// originHandler serves base-URL ("origin") mode. Where the forward-proxy
// path relies on the target honoring HTTPS_PROXY, base-URL mode points the
// target's SDK at faultkit directly via injected *_BASE_URL env vars, so
// the client connects here as if faultkit were the API origin. The handler
// resolves the provider from the request path prefix, reuses the Faulter's
// decision and synthesis, and either writes the synthetic fault (no
// upstream round trip) or reverse-proxies to the real upstream over TLS.
type originHandler struct {
	faulter *Faulter
	rp      *httputil.ReverseProxy
	vlog    inject.Logf
}

// newOriginHandler builds an origin-mode handler that reuses faulter for
// fault decisions and synthesis.
func newOriginHandler(faulter *Faulter, vlog inject.Logf) *originHandler {
	h := &originHandler{faulter: faulter, vlog: vlog}
	h.rp = &httputil.ReverseProxy{
		// Target the real upstream. The outbound path is already the
		// stripped API path (ServeHTTP set it before the proxy runs).
		// Rewrite (not the deprecated Director) omits X-Forwarded-* headers.
		Rewrite: func(pr *httputil.ProxyRequest) {
			p, _ := pr.In.Context().Value(ctxOriginProvider).(provider)
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = p.upstream
			pr.Out.Host = p.upstream
		},
		ModifyResponse: func(res *http.Response) error {
			exp, ok := res.Request.Context().Value(ctxOriginExp).(*scenario.Experiment)
			if !ok || exp == nil || exp.Fault.StreamCutoffTokens == 0 {
				return nil
			}
			p, _ := res.Request.Context().Value(ctxOriginProvider).(provider)
			// As in forward-proxy mode, without a delay the fault fires only
			// if the stream reaches the cut point.
			wrapStreamCutoff(res, exp.Fault.StreamCutoffTokens, func() {
				h.faulter.emitEffect(res.Request, exp, p.upstream)
			}, func(err error) {
				h.faulter.emitErr(res.Request, exp, p.upstream, err)
			})
			return nil
		},
	}
	return h
}

// ServeHTTP implements http.Handler.
func (h *originHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Any request reaching here is traffic the target routed to faultkit.
	h.faulter.observe()

	p, rest, ok := providerForPath(r.URL.Path)
	if !ok {
		http.Error(w, "faultkit: unrecognized base-URL path", http.StatusNotFound)
		return
	}

	// The upstream host is known from the provider, so match on it directly
	// instead of mutating the request to fool the matcher.
	exp := h.faulter.DecideHostPath(r.Method, p.upstream, rest)

	// A delay holds the request before anything else. If Stop or the client
	// cuts it short, drop the connection: nothing is written and nothing is
	// forwarded. r.Context() ends when the client goes away only once
	// net/http has read the request body, so in practice only for requests
	// without one.
	if exp != nil && exp.Fault.DelayMS > 0 && !h.faulter.hold(r, exp, p.upstream, r.Context().Done()) {
		panic(http.ErrAbortHandler)
	}

	// Fully synthetic faults skip the upstream round trip entirely.
	if exp != nil && isSynthetic(exp.Fault) {
		h.faulter.writeSyntheticResponse(w, r, exp, p.upstream)
		return
	}

	// Pass-through (no fault), delay only or stream-cutoff: forward to the
	// real upstream at the stripped API path; ModifyResponse applies the
	// cutoff if set.
	r.URL.Path = rest
	ctx := context.WithValue(r.Context(), ctxOriginProvider, p)
	if exp != nil {
		ctx = context.WithValue(ctx, ctxOriginExp, exp)
	}
	h.rp.ServeHTTP(w, r.WithContext(ctx))
}
