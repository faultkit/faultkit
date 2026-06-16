package proxy

import (
	"context"
	"net/http"
	"net/http/httputil"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/scenario"
)

type originCtxKey int

const (
	ctxOriginProvider originCtxKey = iota
	ctxOriginRest
	ctxOriginExp
)

// originHandler serves base-URL ("origin") mode. Where the forward-proxy
// path relies on the target honoring HTTPS_PROXY, base-URL mode points the
// target's SDK at faultkit directly via injected *_BASE_URL env vars, so
// the client connects here as if faultkit were the API origin. The handler
// resolves the provider from the request path prefix, runs the same fault
// decision as the forward proxy, and either synthesizes the fault (no
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
		// Rewrite (not the deprecated Director) targets the real upstream;
		// it deliberately does not add X-Forwarded-* headers.
		Rewrite: func(pr *httputil.ProxyRequest) {
			p, _ := pr.In.Context().Value(ctxOriginProvider).(provider)
			rest, _ := pr.In.Context().Value(ctxOriginRest).(string)
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = p.upstream
			pr.Out.Host = p.upstream
			pr.Out.URL.Path = rest
		},
		ModifyResponse: func(res *http.Response) error {
			exp, ok := res.Request.Context().Value(ctxOriginExp).(*scenario.Experiment)
			if !ok || exp == nil || exp.Fault.StreamCutoffTokens == 0 {
				return nil
			}
			p, _ := res.Request.Context().Value(ctxOriginProvider).(provider)
			host := normalizeHost(p.upstream)
			wrapStreamCutoff(res, exp.Fault.StreamCutoffTokens, func(err error) {
				h.faulter.emitErr(res.Request, exp, host, err)
			})
			h.faulter.emit(res.Request, exp, host)
			return nil
		},
	}
	return h
}

// ServeHTTP implements http.Handler.
func (h *originHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, rest, ok := providerForPath(r.URL.Path)
	if !ok {
		http.Error(w, "faultkit: unrecognized base-URL path", http.StatusNotFound)
		return
	}

	// Present the real upstream host and the stripped (real) path to the
	// matcher, so existing host/path scenarios fire unchanged in origin mode.
	r.Host = p.upstream
	r.URL.Host = ""
	r.URL.Path = rest

	exp := h.faulter.Decide(r)
	host := normalizeHost(p.upstream)

	// Fully synthetic faults skip the upstream round trip entirely.
	if exp != nil && exp.Fault.StreamCutoffTokens == 0 {
		syn := fixtures.Build(host, exp.Fault)
		for k, v := range syn.Headers {
			w.Header().Set(k, v)
		}
		w.Header().Set(syntheticHeader, "true")
		w.WriteHeader(syn.Status)
		_, _ = w.Write(syn.Body)
		h.faulter.emit(r, exp, host)
		return
	}

	// Pass-through (no fault) or stream-cutoff: forward to the real upstream.
	// ModifyResponse applies the cutoff when exp is set.
	ctx := context.WithValue(r.Context(), ctxOriginProvider, p)
	ctx = context.WithValue(ctx, ctxOriginRest, rest)
	if exp != nil {
		ctx = context.WithValue(ctx, ctxOriginExp, exp)
	}
	h.rp.ServeHTTP(w, r.WithContext(ctx))
}
