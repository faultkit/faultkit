package proxy

import "strings"

// provider describes an LLM API that faultkit can intercept in base-URL
// mode. Where the forward-proxy path relies on the target honoring
// HTTPS_PROXY, base-URL mode points the client's SDK directly at faultkit
// via a provider-specific base-URL environment variable. Each provider
// records:
//
//   - upstream:   the real API host faultkit fronts and forwards to.
//   - baseURLEnv: the env keys whose value an SDK uses as the API origin;
//     SDKs honor these even when they ignore HTTPS_PROXY.
//   - pathPrefix: the prefix faultkit serves this provider under, so a
//     single origin listener can host several providers and still tell
//     them apart (SDKs append the real path after the injected base URL).
//   - apiBase: the API base path the SDK expects the base URL to already
//     include. OpenAI's SDK uses a base URL of https://api.openai.com/v1
//     and appends /chat/completions, so faultkit must inject ".../v1";
//     Anthropic's SDK uses https://api.anthropic.com and appends
//     /v1/messages, so its apiBase is empty. Getting this right is what
//     makes the injected URL line up with the real /v1/... paths scenarios
//     match.
type provider struct {
	id         string
	upstream   string
	baseURLEnv []string
	pathPrefix string
	apiBase    string
}

// providerRegistry is the set of providers base-URL mode understands.
// Ordered for deterministic iteration.
var providerRegistry = []provider{
	{
		id:         "openai",
		upstream:   "api.openai.com",
		baseURLEnv: []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"},
		pathPrefix: "/__fk/openai",
		apiBase:    "/v1",
	},
	{
		id:         "anthropic",
		upstream:   "api.anthropic.com",
		baseURLEnv: []string{"ANTHROPIC_BASE_URL"},
		pathPrefix: "/__fk/anthropic",
		apiBase:    "",
	},
}

// baseURL is the value to inject into this provider's base-URL env vars:
// faultkit's origin listener (addr is host:port), the provider prefix, and
// the API base path the SDK expects (apiBase). The SDK appends the rest of
// the path; e.g. the OpenAI SDK turns ".../__fk/openai/v1" into a request
// to ".../__fk/openai/v1/chat/completions", which faultkit strips to
// "/v1/chat/completions" before matching.
func (p provider) baseURL(addr string) string {
	return "http://" + addr + p.pathPrefix + p.apiBase
}

// providerForPath resolves an incoming origin-mode request path to its
// provider and returns the remaining path with the prefix stripped (so
// the matcher and upstream forwarder see the real API path). ok is false
// if no provider prefix matches.
func providerForPath(path string) (p provider, rest string, ok bool) {
	for _, cand := range providerRegistry {
		if path == cand.pathPrefix || strings.HasPrefix(path, cand.pathPrefix+"/") {
			rest = strings.TrimPrefix(path, cand.pathPrefix)
			if rest == "" {
				rest = "/"
			}
			return cand, rest, true
		}
	}
	return provider{}, "", false
}

// providerIDList comma-joins the ids of ps.
func providerIDList(ps []provider) string {
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.id
	}
	return strings.Join(ids, ", ")
}

// providersForHostGlobs returns the registered providers whose upstream
// host is targeted by any of the given host globs (e.g. a scenario's
// match.host clauses). It uses the same glob semantics as the matcher, so
// "api.openai.com" selects OpenAI and "api.*.com" selects both. The result
// preserves registry order and is deduplicated.
func providersForHostGlobs(globs []string) []provider {
	var out []provider
	for _, p := range providerRegistry {
		for _, g := range globs {
			if g != "" && globMatch(g, p.upstream) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
