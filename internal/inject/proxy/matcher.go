package proxy

import (
	"net/http"

	"github.com/faultkit/faultkit/pkg/scenario"
)

// Matcher selects an experiment for an HTTP request based on host and
// path globs. Experiments are evaluated in YAML order; the first match
// wins. Syscall-only experiments are dropped at construction time —
// they don't apply to proxy traffic.
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
	path := ""
	if req.URL != nil {
		path = req.URL.Path
	}
	return m.matchHostPath(hostFromRequest(req), path)
}

// matchHostPath returns the first experiment matching host and path. The
// host is normalized here, so callers may pass it raw (forward proxy, via
// Match) or already-resolved (base-URL/origin mode, which knows the
// upstream host directly and need not smuggle it through a request).
func (m *Matcher) matchHostPath(host, path string) *scenario.Experiment {
	if m == nil {
		return nil
	}
	host = normalizeHost(host)
	for i := range m.experiments {
		exp := &m.experiments[i]
		if !globMatch(exp.Match.Host, host) {
			continue
		}
		if exp.Match.Path != "" && !globMatch(exp.Match.Path, path) {
			continue
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

// globMatch reports whether s matches pattern. `*` matches any run of
// characters (including `/`), `?` matches any single character. Unlike
// filepath.Match, `*` is not stopped by separators — `/v1/*` matches
// `/v1/chat/completions`. No bracket character classes; YAML scenarios
// don't need them.
func globMatch(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatch(pattern, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		default:
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		}
	}
	return len(s) == 0
}
