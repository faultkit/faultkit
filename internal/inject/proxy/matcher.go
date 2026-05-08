package proxy

import (
	"net/http"
	"path/filepath"

	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

// Matcher selects an experiment for an HTTP request based on host and
// path globs (filepath.Match syntax). Experiments are evaluated in
// YAML order; the first match wins. Syscall-only experiments are
// dropped at construction time — they don't apply to proxy traffic.
type Matcher struct {
	experiments []scenario.Experiment
}

// NewMatcher returns a Matcher containing only the HTTP experiments
// from s.
func NewMatcher(s *scenario.Scenario) *Matcher {
	if s == nil {
		return &Matcher{}
	}
	httpExp := make([]scenario.Experiment, 0, len(s.Experiments))
	for _, exp := range s.Experiments {
		if exp.Match.IsHTTP() {
			httpExp = append(httpExp, exp)
		}
	}
	return &Matcher{experiments: httpExp}
}

// Match returns the first experiment whose match clause fits req, or
// nil if none does. CONNECT requests are skipped — martian runs
// modifiers on the tunnel-establishment CONNECT before MITM, and we
// only want to fault the inner HTTPS request, not the tunnel itself.
func (m *Matcher) Match(req *http.Request) *scenario.Experiment {
	if m == nil || len(m.experiments) == 0 || req.Method == http.MethodConnect {
		return nil
	}
	host := normalizeHost(hostFromRequest(req))
	path := ""
	if req.URL != nil {
		path = req.URL.Path
	}
	for i := range m.experiments {
		exp := &m.experiments[i]
		if ok, _ := filepath.Match(exp.Match.Host, host); !ok {
			continue
		}
		if exp.Match.Path != "" {
			if ok, _ := filepath.Match(exp.Match.Path, path); !ok {
				continue
			}
		}
		return exp
	}
	return nil
}

func hostFromRequest(req *http.Request) string {
	if req.URL != nil && req.URL.Host != "" {
		return req.URL.Host
	}
	return req.Host
}
