package fixtures

import "sort"

// Fixture is the provider-specific realization of a failure mode: the
// concrete response an SDK for that provider would parse. A failure mode is
// provider-agnostic; the Fixture supplies the per-provider shape (which API
// path it applies to, the status/headers, and either a verbatim body or a
// streaming-cutoff token count). The upstream host is not stored here — the
// expansion step joins each fixture with the provider registry, which owns
// hosts. An empty Body for a 4xx/5xx fixture means "synthesize the
// vendor-accurate error body" via Build at fault time.
type Fixture struct {
	Path               string
	Status             int
	Headers            map[string]string
	Body               string
	StreamCutoffTokens int
}

// catalog maps failure-mode id → provider id → Fixture. Provider ids must
// match the proxy provider registry (see TestCatalogProvidersAreRegistered).
var catalog = map[string]map[string]Fixture{
	"rate-limited": {
		"openai":    {Path: "/v1/*", Status: 429, Headers: map[string]string{"Retry-After": "30"}},
		"anthropic": {Path: "/v1/*", Status: 429, Headers: map[string]string{"Retry-After": "30"}},
	},
	"malformed-json": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIMalformedJSON},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicMalformedJSON},
	},
	"streaming-cutoff": {
		"openai":    {Path: "/v1/chat/completions", StreamCutoffTokens: 80},
		"anthropic": {Path: "/v1/messages", StreamCutoffTokens: 80},
	},
	// overloaded is Anthropic-specific: HTTP 529 overloaded_error has no
	// OpenAI equivalent. Body synthesized from the anthropic 529 fixture.
	"overloaded": {
		"anthropic": {Path: "/v1/*", Status: 529},
	},
	"max-tokens-truncation": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAITruncated},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicTruncated},
	},
	"malformed-tool-use": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIMalformedToolUse},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicMalformedToolUse},
	},
}

// For returns the fixture for (mode, provider) and whether one exists.
func For(mode, provider string) (Fixture, bool) {
	byProvider, ok := catalog[mode]
	if !ok {
		return Fixture{}, false
	}
	f, ok := byProvider[provider]
	return f, ok
}

// KnownMode reports whether mode is in the catalog.
func KnownMode(mode string) bool {
	_, ok := catalog[mode]
	return ok
}

// Modes returns every failure-mode id in the catalog, sorted.
func Modes() []string {
	out := make([]string, 0, len(catalog))
	for m := range catalog {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// The malformed-json bodies: a valid provider envelope whose assistant
// content is itself syntactically invalid JSON (trailing comma), so the
// agent's structured-output parser is what fails. Kept verbatim from the
// pre-migration builtin YAML for behavioral parity.
const (
	openAIMalformedJSON    = `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"{\"action\":\"lookup\",\"args\":{\"id\":42},}"},"finish_reason":"stop"}]}`
	anthropicMalformedJSON = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"{\"action\":\"lookup\",\"args\":{\"id\":42},}"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":8}}`
)

// max-tokens-truncation: a 200 whose content is valid but truncated, flagged by
// finish_reason "length" (OpenAI) / stop_reason "max_tokens" (Anthropic). The
// failure surfaces when an agent treats the partial content as a complete answer.
const (
	openAITruncated    = `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"The answer is"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":16,"total_tokens":26}}`
	anthropicTruncated = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"The answer is"}],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":16}}`
)

// malformed-tool-use: a tool call the agent will mis-dispatch. OpenAI delivers
// arguments as a JSON *string*, so the malformed case makes that string invalid
// JSON. Anthropic delivers input as a JSON *object*, so the equivalent failure
// is a schema-violating value (id typed as a string where an int is expected).
const (
	openAIMalformedToolUse    = `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup_user","arguments":"{\"id\": 42,}"}}]},"finish_reason":"tool_calls"}]}`
	anthropicMalformedToolUse = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"tool_use","id":"toolu_1","name":"lookup_user","input":{"id":"not-an-integer"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":8}}`
)
