package proxy_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

const (
	mcpInitializeResult = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"weather","version":"1.0.0"}}`
	mcpToolsListResult  = `{"tools":[{"name":"get_weather","inputSchema":{"type":"object"},` +
		`"outputSchema":{"type":"object","properties":{"temperature":{"type":"number"},"conditions":{"type":"string"}},"required":["temperature","conditions"]}}]}`
	mcpCallResult = `{"content":[{"type":"text","text":"{\"temperature\":22.5,\"conditions\":\"sunny\"}"}],` +
		`"structuredContent":{"temperature":22.5,"conditions":"sunny"},"isError":false}`
	// mcpCallMutated is mcpCallResult after mcp-schema-mismatch: the
	// members are sorted, the structuredContent values have new types, and
	// content[].text is as sent.
	mcpCallMutated = `{"content":[{"type":"text","text":"{\"temperature\":22.5,\"conditions\":\"sunny\"}"}],` +
		`"isError":false,"structuredContent":{"conditions":true,"temperature":"22.5"}}`
	mcpCallTextOnly = `{"content":[{"type":"text","text":"22.5 C, sunny"}],"isError":false}`
	mcpProgress     = `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":1,"progress":0.5}}`
)

var mcpResults = map[string]string{
	"initialize": mcpInitializeResult,
	"tools/list": mcpToolsListResult,
	"tools/call": mcpCallResult,
}

func mcpMessage(id, result string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"result":` + result + `}`
}

// mcpSSE is msg as a streamable-HTTP response stream: a progress
// notification, then msg, with CRLF line endings.
func mcpSSE(msg string) string {
	return "event: message\r\ndata: " + mcpProgress + "\r\n\r\nevent: message\r\ndata: " + msg + "\r\n\r\n"
}

// mcpUpstream is a minimal streamable-HTTP MCP server. It answers each
// JSON-RPC request with the result results holds for its method, echoing
// the request id, as application/json or, with sse set, as mcpSSE. With
// chunked set it flushes the SSE headers first, so the stream is sent in
// chunks as servers stream it; otherwise it has a Content-Length. It
// records each request body as it arrived.
type mcpUpstream struct {
	sse     bool
	chunked bool
	results map[string]string

	mu     sync.Mutex
	bodies []string
}

func (u *mcpUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.mu.Lock()
	u.bodies = append(u.bodies, string(body))
	u.mu.Unlock()
	var msg struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res := mcpMessage(string(msg.ID), u.results[msg.Method])
	if u.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok && u.chunked {
			f.Flush()
		}
		_, _ = io.WriteString(w, mcpSSE(res))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, res)
}

func (u *mcpUpstream) received() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.bodies)
}

func mcpScenario(fault faulttypes.Fault) *scenario.Scenario {
	return &scenario.Scenario{
		Name: "test-mcp-schema-mismatch",
		Experiments: []scenario.Experiment{{
			Name:        "schema-mismatch",
			Fault:       fault,
			Match:       scenario.Match{Host: "127.0.0.1", Path: "/mcp"},
			Probability: 1.0,
		}},
	}
}

func mcpRequest(id, method, params string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `","params":` + params + `}`
}

// mcpPost sends body to url as a streamable-HTTP client does and returns
// the response and its body.
func mcpPost(t *testing.T, client *http.Client, url, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(got)
}

// assertMCPEvents drains events and checks they are exactly want fired
// events of the mutation experiment on /mcp.
func assertMCPEvents(t *testing.T, events <-chan inject.Event, want int) {
	t.Helper()
	got := 0
	for ev := range events {
		if !ev.Fired || ev.Err != "" || ev.Experiment != "schema-mismatch" || ev.Path != "/mcp" {
			t.Errorf("unexpected event %+v", ev)
			continue
		}
		if ev.Fault.ResponseMutation != "mcp-schema-mismatch" {
			t.Errorf("event fault = %+v, want the mutation", ev.Fault)
		}
		got++
	}
	if got != want {
		t.Errorf("fired events = %d, want %d", got, want)
	}
}

// mcp-schema-mismatch through the forward proxy: the tools/call result's
// structuredContent changes type, in a JSON body and in an SSE stream, with
// the request id kept and one fired event. initialize and tools/list on the
// same host and path, matched by the same experiment, pass through
// untouched and fire nothing. Every request reaches the server unchanged.
func TestE2E_MCPSchemaMismatch(t *testing.T) {
	cases := []struct {
		name    string
		sse     bool
		chunked bool
		callID  string
	}{
		{name: "application/json", callID: `2`},
		{name: "text/event-stream, chunked", sse: true, chunked: true, callID: `"call-2"`},
		{name: "text/event-stream, with a length", sse: true, callID: `"call-2"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &mcpUpstream{sse: tc.sse, chunked: tc.chunked, results: mcpResults}
			inj, upstream, client := e2eInjector(t, mcpScenario(faulttypes.Fault{ResponseMutation: "mcp-schema-mismatch"}), up)
			wrap := func(msg string) string {
				if tc.sse {
					return mcpSSE(msg)
				}
				return msg
			}

			sent := []string{
				mcpRequest(`0`, "initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}`),
				mcpRequest(`1`, "tools/list", `{}`),
				mcpRequest(tc.callID, "tools/call", `{"name":"get_weather","arguments":{"city":"Paris"}}`),
			}
			want := []string{
				wrap(mcpMessage(`0`, mcpInitializeResult)),
				wrap(mcpMessage(`1`, mcpToolsListResult)),
				wrap(`{"id":` + tc.callID + `,"jsonrpc":"2.0","result":` + mcpCallMutated + `}`),
			}
			for i, body := range sent {
				resp, got := mcpPost(t, client, upstream.URL+"/mcp", body)
				if got != want[i] {
					t.Errorf("request %d: client got\n%s\nwant\n%s", i, got, want[i])
				}
				if !tc.sse && resp.ContentLength != int64(len(got)) {
					t.Errorf("request %d: Content-Length %d, body %d bytes", i, resp.ContentLength, len(got))
				}
			}

			if err := inj.Stop(context.Background()); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			assertMCPEvents(t, inj.Events(), 1)
			if got := up.received(); !slices.Equal(got, sent) {
				t.Errorf("upstream received %q, want the requests unchanged", got)
			}
		})
	}
}

// Requests the mutation can't apply to pass through untouched and fire
// nothing: a tools/call whose result has no structuredContent, and a
// tools/call whose request body is too large for faultkit to read, so its
// id stays unknown. The large body still reaches the server whole.
func TestE2E_MCPSchemaMismatchPassesThrough(t *testing.T) {
	bigArgs := `{"name":"summarize","arguments":{"text":"` + strings.Repeat("x", 1<<20) + `"}}`
	cases := []struct {
		name    string
		sse     bool
		results map[string]string
		request string
		want    string
	}{
		{
			name:    "no structuredContent, JSON",
			results: map[string]string{"tools/call": mcpCallTextOnly},
			request: mcpRequest(`5`, "tools/call", `{"name":"get_weather","arguments":{}}`),
			want:    mcpMessage(`5`, mcpCallTextOnly),
		},
		{
			name:    "no structuredContent, SSE",
			sse:     true,
			results: map[string]string{"tools/call": mcpCallTextOnly},
			request: mcpRequest(`5`, "tools/call", `{"name":"get_weather","arguments":{}}`),
			want:    mcpSSE(mcpMessage(`5`, mcpCallTextOnly)),
		},
		{
			name:    "request body over 1 MiB",
			results: mcpResults,
			request: mcpRequest(`6`, "tools/call", bigArgs),
			want:    mcpMessage(`6`, mcpCallResult),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &mcpUpstream{sse: tc.sse, results: tc.results}
			inj, upstream, client := e2eInjector(t, mcpScenario(faulttypes.Fault{ResponseMutation: "mcp-schema-mismatch"}), up)

			if _, got := mcpPost(t, client, upstream.URL+"/mcp", tc.request); got != tc.want {
				t.Errorf("client got %d bytes, want the %d upstream bytes unchanged", len(got), len(tc.want))
			}

			if err := inj.Stop(context.Background()); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			assertMCPEvents(t, inj.Events(), 0)
			if got := up.received(); len(got) != 1 || got[0] != tc.request {
				t.Errorf("upstream received %d requests, want the one sent, unchanged", len(got))
			}
		})
	}
}

// With delay_ms, the request is held first and counts as fired when the
// hold starts; the mutation then still applies but isn't counted again.
func TestE2E_MCPSchemaMismatchWithDelayFiresOnce(t *testing.T) {
	const delay = 50 * time.Millisecond
	up := &mcpUpstream{results: mcpResults}
	inj, upstream, client := e2eInjector(t, mcpScenario(faulttypes.Fault{DelayMS: 50, ResponseMutation: "mcp-schema-mismatch"}), up)

	start := time.Now()
	_, got := mcpPost(t, client, upstream.URL+"/mcp", mcpRequest(`9`, "tools/call", `{"name":"get_weather","arguments":{}}`))
	if elapsed := time.Since(start); elapsed < delay {
		t.Errorf("response after %v, want at least the %v delay", elapsed, delay)
	}
	if want := `{"id":9,"jsonrpc":"2.0","result":` + mcpCallMutated + `}`; got != want {
		t.Errorf("client got\n%s\nwant\n%s", got, want)
	}

	if err := inj.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertMCPEvents(t, inj.Events(), 1)
}
