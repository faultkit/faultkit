package fixtures

import "sort"

// Fixture is the provider-specific realization of a failure mode: the
// concrete response an SDK for that provider would parse. A failure mode is
// provider-agnostic; the Fixture supplies the per-provider shape (which API
// path it applies to, the status/headers, and either a verbatim body or a
// streaming-cutoff token count), optionally after a delay; a delay alone
// forwards the request unchanged. The upstream host is not stored here — the
// expansion step joins each fixture with the provider registry, which owns
// hosts. An empty Body for a 4xx/5xx fixture means "synthesize the
// vendor-accurate error body" via Build at fault time.
type Fixture struct {
	Path               string
	Status             int
	Headers            map[string]string
	Body               string
	StreamCutoffTokens int
	DelayMS            int
	DelayJitterMS      int
}

// catalog maps failure-mode id → provider id → Fixture. Provider ids must
// match the proxy provider registry (see TestCatalogProvidersAreRegistered).
var catalog = map[string]map[string]Fixture{
	"rate-limited": {
		"openai":    {Path: "/v1/*", Status: 429, Headers: map[string]string{"Retry-After": "30"}},
		"anthropic": {Path: "/v1/*", Status: 429, Headers: map[string]string{"Retry-After": "30"}},
		"bedrock":   {Path: "/model/*", Status: 429, Headers: map[string]string{"x-amzn-errortype": "ThrottlingException"}},
	},
	"malformed-json": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIMalformedJSON},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicMalformedJSON},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseMalformedJSON},
	},
	"streaming-cutoff": {
		"openai":    {Path: "/v1/chat/completions", StreamCutoffTokens: 80},
		"anthropic": {Path: "/v1/messages", StreamCutoffTokens: 80},
	},
	// slow-first-token holds the request 8–15 s, then forwards it unchanged,
	// so the response (streamed or not) starts that much later.
	"slow-first-token": {
		"openai":    {Path: "/v1/chat/completions", DelayMS: 8000, DelayJitterMS: 7000},
		"anthropic": {Path: "/v1/messages", DelayMS: 8000, DelayJitterMS: 7000},
		"bedrock":   {Path: "/model/*", DelayMS: 8000, DelayJitterMS: 7000},
	},
	// overloaded is Anthropic-specific: HTTP 529 overloaded_error has no
	// OpenAI equivalent. Body synthesized from the anthropic 529 fixture.
	"overloaded": {
		"anthropic": {Path: "/v1/*", Status: 529},
	},
	"max-tokens-truncation": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAITruncated},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicTruncated},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseTruncated},
	},
	"malformed-tool-use": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIMalformedToolUse},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicMalformedToolUse},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseMalformedToolUse},
	},
	// empty-response: a 200 whose envelope parses but carries no usable
	// content. Like the other JSON-body fixtures it answers with non-streaming
	// JSON, so it targets non-streaming calls.
	"empty-response": {
		"openai":    {Path: "/v1/chat/completions", Status: 200, Body: openAIEmptyResponse},
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicEmptyResponse},
		"bedrock":   {Path: "/model/*/converse", Status: 200, Body: bedrockConverseEmptyResponse},
	},
	// embeddings-unavailable: the embeddings endpoint returns 503; bodies are
	// synthesized per vendor. Anthropic has no embeddings API, so no cell. The
	// Bedrock glob selects InvokeModel on embedding models
	// (e.g. amazon.titan-embed-text-v2:0, cohere.embed-english-v3).
	"embeddings-unavailable": {
		"openai":  {Path: "/v1/embeddings", Status: 503},
		"bedrock": {Path: "/model/*embed*/invoke", Status: 503, Headers: map[string]string{"x-amzn-errortype": "ServiceUnavailableException"}},
	},
	// context-length-exceeded: the provider rejects the request as over the
	// model's context window with a 400. The body is explicit here rather than
	// a 400 entry in the per-vendor error tables, which would change what every
	// raw `http_status: 400` fault on these hosts returns.
	"context-length-exceeded": {
		"openai":    {Path: "/v1/chat/completions", Status: 400, Body: openAIContextLengthExceeded},
		"anthropic": {Path: "/v1/messages", Status: 400, Body: anthropicContextLengthExceeded},
		"bedrock":   {Path: "/model/*/converse", Status: 400, Headers: map[string]string{"x-amzn-errortype": "ValidationException"}, Body: bedrockContextLengthExceeded},
	},
	// Anthropic-distinctive modes (no cross-provider equivalent yet).
	"stream-error": {
		"anthropic": {Path: "/v1/messages", Status: 200, Headers: map[string]string{"Content-Type": "text/event-stream"}, Body: anthropicStreamError},
	},
	"tool-use-cutoff": {
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicToolUseCutoff},
	},
	"refusal": {
		"anthropic": {Path: "/v1/messages", Status: 200, Body: anthropicRefusal},
	},
	"request-too-large": {
		"anthropic": {Path: "/v1/*", Status: 413},
	},
	// Bedrock-distinctive modes (no cross-provider equivalent). Single-provider
	// fixtures → they only fire via the bedrock-* scenarios, never leak into
	// llm-api-degraded / anthropic-* scenarios.
	"model-timeout": {
		"bedrock": {Path: "/model/*", Status: 408, Headers: map[string]string{"x-amzn-errortype": "ModelTimeoutException"}},
	},
	"service-unavailable": {
		"bedrock": {Path: "/model/*", Status: 503, Headers: map[string]string{"x-amzn-errortype": "ServiceUnavailableException"}},
	},
	// Vector-DB modes (forward-proxy only). The pinecone and qdrant providers
	// have no base-URL env, so these fire only through HTTPS_PROXY. Single
	// endpoint per provider: Pinecone's vector query and Qdrant's Query API
	// (not the legacy /points/search, nor /points/query/batch, whose bodies
	// differ).
	"stale-results": {
		"pinecone": {Path: "/query", Status: 200, Body: pineconeStaleResults},
		"qdrant":   {Path: "/collections/*/points/query", Status: 200, Body: qdrantStaleResults},
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

// Providers returns every provider id that appears anywhere in the catalog,
// sorted and de-duplicated. Used to assert every catalog provider id is a
// registered proxy provider — a typo'd id would otherwise make fan-out
// silently skip that fixture.
func Providers() []string {
	seen := map[string]bool{}
	for _, byProvider := range catalog {
		for id := range byProvider {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
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

// anthropicStreamError is a well-formed Anthropic SSE stream that emits an
// `event: error` (overloaded_error) partway through and then stops — no
// `message_stop`. Tests whether the consumer parses SSE error events instead
// of treating the byte-clean stream end as a successful completion.
const anthropicStreamError = `event: message_start
data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The answer "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"is"}}

event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

`

// anthropicToolUseCutoff is a valid envelope carrying a tool_use block whose
// turn was truncated: stop_reason is "max_tokens" (not "tool_use"), so the tool
// call is incomplete and must NOT be dispatched.
const anthropicToolUseCutoff = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"tool_use","id":"toolu_1","name":"lookup_user","input":{"id":42}}],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":16}}`

// anthropicRefusal is a 200 whose stop_reason is "refusal" — the model declined.
// Agents should treat this distinctly from a normal completion.
const anthropicRefusal = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"I can't help with that request."}],"stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":8}}`

// empty-response: a 200 with nothing to act on. OpenAI returns content null
// with finish_reason "content_filter"; Anthropic returns an empty content array
// with an ordinary "end_turn". An agent that indexes the first content block or
// stores the text unchecked fails here.
const (
	openAIEmptyResponse    = `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":10,"completion_tokens":0,"total_tokens":10}}`
	anthropicEmptyResponse = `{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4","content":[],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":0}}`
)

// context-length-exceeded: the vendor's 400 for a prompt over the context
// window. OpenAI uses its standard error envelope with code
// "context_length_exceeded"; Anthropic uses invalid_request_error with a
// "prompt is too long" message.
const (
	openAIContextLengthExceeded    = `{"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 131072 tokens. Please reduce the length of the messages.","type":"invalid_request_error","param":"messages","code":"context_length_exceeded"}}`
	anthropicContextLengthExceeded = `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`
)
