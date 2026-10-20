package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// White-box: the mutation, the tools/call detection and the SSE forwarder
// are unexported.

// mcpResult is a tools/call result whose structuredContent holds one
// top-level value of every JSON type. content[0].text carries the same data
// as text, as MCP servers send it for clients that don't read
// structuredContent.
const mcpResult = `{"content":[{"type":"text","text":"{\"temp\":22.5,\"unit\":\"C\"}"}],` +
	`"structuredContent":{"temp":22.5,"unit":"C","count":"3","ok":true,"none":null,` +
	`"station":{"id":7,"tags":["a","b"]},"readings":[1,2]},"isError":false,"_meta":{"trace":"<x>"}}`

// mcpMutated is mcpResult after mcp-schema-mismatch, keys sorted: every
// structuredContent value has a new type, and everything else is as sent.
const mcpMutated = `{"_meta":{"trace":"<x>"},"content":[{"type":"text","text":"{\"temp\":22.5,\"unit\":\"C\"}"}],"isError":false,` +
	`"structuredContent":{"count":3,"none":"null","ok":"true","readings":"[1,2]","station":"{\"id\":7,\"tags\":[\"a\",\"b\"]}","temp":"22.5","unit":true}}`

func rpcResponse(id, result string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"result":` + result + `}`
}

func TestChangeType(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`42`, `"42"`},
		{`-1.5e3`, `"-1.5e3"`},
		{`"42"`, `42`},
		{`"3.25"`, `3.25`},
		{`"sunny"`, `true`},
		{`""`, `true`},
		{`"NaN"`, `true`},
		{`"1e999"`, `true`},
		{`true`, `"true"`},
		{`false`, `"false"`},
		{`null`, `"null"`},
		{`[1, 2]`, `"[1,2]"`},
		{`{"a": {"b": [1, "<c>"]}}`, `"{\"a\":{\"b\":[1,\"<c>\"]}}"`},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := changeType([]byte(c.in))
			if err != nil {
				t.Fatalf("changeType: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("changeType(%s) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

// mustRPCID decodes raw, the JSON text of a JSON-RPC id, as the proxy does.
func mustRPCID(t *testing.T, raw string) any {
	t.Helper()
	id, ok := rpcID(json.RawMessage(raw))
	if !ok {
		t.Fatalf("rpcID(%s): not an id", raw)
	}
	return id
}

// Ids are equal exactly when they are the same string, or numbers of the
// same value however they are written. Anything but a string or a number,
// or a number with an absurd exponent, isn't an id.
func TestRPCID(t *testing.T) {
	same := [][2]string{
		{`"call-7"`, `"call-7"`},
		{`7`, `7.0`},
		{`7`, `0.7e1`},
		{`7`, `70E-1`},
		{`700`, `7e+2`},
		{`-7`, `-7.00`},
		{`0`, `-0`},
		{`0`, `0.000e5`},
		{`1e999`, `10e998`},
		{`9007199254740993`, `9007199254740993.0`},
	}
	for _, p := range same {
		if a, b := mustRPCID(t, p[0]), mustRPCID(t, p[1]); a != b {
			t.Errorf("ids %s and %s differ (%v, %v), want the same id", p[0], p[1], a, b)
		}
	}
	different := [][2]string{
		{`9007199254740993`, `9007199254740992`},
		{`7`, `"7"`},
		{`7`, `-7`},
		{`1e999`, `1e998`},
		{`0.1`, `1`},
		{`"a"`, `"A"`},
	}
	for _, p := range different {
		if a, b := mustRPCID(t, p[0]), mustRPCID(t, p[1]); a == b {
			t.Errorf("ids %s and %s are the same (%v), want different ids", p[0], p[1], a)
		}
	}
	for _, raw := range []string{``, `null`, `true`, `{}`, `[7]`, `1e123456789`, `1e-000000000123456789`} {
		if id, ok := rpcID(json.RawMessage(raw)); ok {
			t.Errorf("rpcID(%s) = %v, want not an id", raw, id)
		}
	}
}

// mutateToolResult changes the type of each top-level structuredContent
// value of the response to the given id and keeps everything else: the
// id, the other result members, content[].text and nested values (which
// become JSON text, not other values). Any other message is left alone.
func TestMutateToolResult(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		id   string // the request's id, as JSON
		want string // empty: not mutated
	}{
		{
			name: "numeric id",
			msg:  rpcResponse(`7`, mcpResult),
			id:   `7`,
			want: `{"id":7,"jsonrpc":"2.0","result":` + mcpMutated + `}`,
		},
		{
			name: "string id",
			msg:  rpcResponse(`"call-7"`, mcpResult),
			id:   `"call-7"`,
			want: `{"id":"call-7","jsonrpc":"2.0","result":` + mcpMutated + `}`,
		},
		{
			name: "numeric id written differently",
			msg:  rpcResponse(`0.7e1`, `{"structuredContent":{"n":1}}`),
			id:   `7`,
			want: `{"id":0.7e1,"jsonrpc":"2.0","result":{"structuredContent":{"n":"1"}}}`,
		},
		{
			name: "integer beyond float64 precision",
			msg:  rpcResponse(`9007199254740993`, `{"structuredContent":{"n":1}}`),
			id:   `9007199254740993`,
			want: `{"id":9007199254740993,"jsonrpc":"2.0","result":{"structuredContent":{"n":"1"}}}`,
		},
		{name: "neighbouring integer beyond float64 precision", msg: rpcResponse(`9007199254740992`, mcpResult), id: `9007199254740993`},
		{name: "other id", msg: rpcResponse(`8`, mcpResult), id: `7`},
		{name: "string id that looks like the number", msg: rpcResponse(`"7"`, mcpResult), id: `7`},
		{name: "no id", msg: `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`, id: `7`},
		{name: "server request with the same id", msg: `{"jsonrpc":"2.0","id":7,"method":"sampling/createMessage","params":{}}`, id: `7`},
		{name: "error response", msg: `{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"bad"}}`, id: `7`},
		{name: "no structuredContent", msg: rpcResponse(`7`, `{"content":[{"type":"text","text":"22.5"}]}`), id: `7`},
		{name: "empty structuredContent", msg: rpcResponse(`7`, `{"structuredContent":{}}`), id: `7`},
		{name: "null structuredContent", msg: rpcResponse(`7`, `{"structuredContent":null}`), id: `7`},
		{name: "structuredContent not an object", msg: rpcResponse(`7`, `{"structuredContent":[1,2]}`), id: `7`},
		{name: "null result", msg: `{"jsonrpc":"2.0","id":7,"result":null}`, id: `7`},
		{name: "batch", msg: `[` + rpcResponse(`7`, mcpResult) + `]`, id: `7`},
		{name: "malformed JSON", msg: strings.TrimSuffix(rpcResponse(`7`, mcpResult), "}"), id: `7`},
		{name: "not JSON", msg: `event: message`, id: `7`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := mutateToolResult([]byte(c.msg), mustRPCID(t, c.id))
			if want := c.want != ""; ok != want {
				t.Fatalf("mutated = %v, want %v (got %s)", ok, want, got)
			}
			if ok && string(got) != c.want {
				t.Errorf("mutated message:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// toolsCallID reports the JSON-RPC id of a tools/call request, and only of
// one, and always leaves the body as it was, read error included.
func TestToolsCallID(t *testing.T) {
	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":{"q":"` +
		strings.Repeat("x", maxMutatedBody) + `"}}}`
	cases := []struct {
		name          string
		body          string
		contentLength int64  // 0: len(body)
		wantID        string // the id, as JSON; empty: not a tools/call request
	}{
		{name: "tools/call with a numeric id", body: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"weather"}}`, wantID: `3`},
		{name: "tools/call with a string id", body: `{"jsonrpc":"2.0","id":"a-3","method":"tools/call"}`, wantID: `"a-3"`},
		{name: "initialize", body: `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`},
		{name: "tools/list", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{name: "tools/call without an id", body: `{"jsonrpc":"2.0","method":"tools/call"}`},
		{name: "tools/call with a null id", body: `{"jsonrpc":"2.0","id":null,"method":"tools/call"}`},
		{name: "method in another case", body: `{"jsonrpc":"2.0","id":3,"Method":"tools/call"}`},
		{name: "batch", body: `[{"jsonrpc":"2.0","id":3,"method":"tools/call"}]`},
		{name: "malformed JSON", body: `{"jsonrpc":"2.0","id":3,"method":"tools/call"`},
		{name: "over the limit, length known", body: huge},
		{name: "over the limit, chunked", body: huge, contentLength: -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", strings.NewReader(c.body))
			if c.contentLength != 0 {
				req.ContentLength = c.contentLength
			}
			id, ok := toolsCallID(req)
			switch {
			case c.wantID == "" && ok:
				t.Errorf("toolsCallID = %v, want not a tools/call request", id)
			case c.wantID != "" && (!ok || id != mustRPCID(t, c.wantID)):
				t.Errorf("toolsCallID = %v, %v; want the id %s", id, ok, c.wantID)
			}
			got, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read restored body: %v", err)
			}
			if string(got) != c.body {
				t.Errorf("restored body is %d bytes, want the %d sent, unchanged", len(got), len(c.body))
			}
		})
	}

	t.Run("read error", func(t *testing.T) {
		readErr := errors.New("client reset")
		const sent = `{"jsonrpc":"2.0","id":3,`
		req := httptest.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", nil)
		req.Body = io.NopCloser(&errAfterReader{r: strings.NewReader(sent), err: readErr})
		req.ContentLength = -1
		if _, ok := toolsCallID(req); ok {
			t.Error("a body that failed to read counts as tools/call")
		}
		got, err := io.ReadAll(req.Body)
		if string(got) != sent || !errors.Is(err, readErr) {
			t.Errorf("restored body = %q, %v; want %q, then %v", got, err, sent, readErr)
		}
	})
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"application/json"}, "Content-Length": []string{strconv.Itoa(len(body))}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// A JSON body is mutated in place, with its length fixed; anything that
// can't be the tools/call result passes through byte for byte.
func TestMutateMCPResponseJSON(t *testing.T) {
	id := mustRPCID(t, `5`)
	tooBig := rpcResponse(`5`, `{"structuredContent":{"blob":"`+strings.Repeat("x", maxMutatedBody)+`"}}`)
	cases := []struct {
		name    string
		res     func() *http.Response
		body    string
		mutated bool
	}{
		{name: "tools/call result", res: func() *http.Response { return jsonResponse(rpcResponse(`5`, mcpResult)) }, body: rpcResponse(`5`, mcpResult), mutated: true},
		{
			name: "media type with parameters",
			res: func() *http.Response {
				r := jsonResponse(rpcResponse(`5`, mcpResult))
				r.Header.Set("Content-Type", "Application/JSON; charset=utf-8")
				return r
			},
			body: rpcResponse(`5`, mcpResult), mutated: true,
		},
		{name: "no structuredContent", res: func() *http.Response { return jsonResponse(rpcResponse(`5`, `{"content":[]}`)) }, body: rpcResponse(`5`, `{"content":[]}`)},
		{name: "over the limit", res: func() *http.Response { return jsonResponse(tooBig) }, body: tooBig},
		{
			name: "over the limit, chunked",
			res: func() *http.Response {
				r := jsonResponse(tooBig)
				r.ContentLength = -1
				return r
			},
			body: tooBig,
		},
		{
			name: "compressed",
			res: func() *http.Response {
				r := jsonResponse(rpcResponse(`5`, mcpResult))
				r.Header.Set("Content-Encoding", "gzip")
				return r
			},
			body: rpcResponse(`5`, mcpResult),
		},
		{
			name: "not JSON",
			res: func() *http.Response {
				r := jsonResponse(rpcResponse(`5`, mcpResult))
				r.Header.Set("Content-Type", "text/plain")
				return r
			},
			body: rpcResponse(`5`, mcpResult),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := c.res()
			var calls int
			mutateMCPResponse(res, id, func() { calls++ })
			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			want := c.body
			if c.mutated {
				want = `{"id":5,"jsonrpc":"2.0","result":` + mcpMutated + `}`
			}
			if string(got) != want {
				t.Errorf("body is %d bytes, want %d; head %q", len(got), len(want), got[:min(len(got), 120)])
			}
			wantCalls := 0
			if c.mutated {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Errorf("onMutate calls = %d, want %d", calls, wantCalls)
			}
			if c.mutated {
				if res.ContentLength != int64(len(got)) || res.Header.Get("Content-Length") != strconv.Itoa(len(got)) {
					t.Errorf("ContentLength %d, header %q; want both %d", res.ContentLength, res.Header.Get("Content-Length"), len(got))
				}
			}
		})
	}
}

// sseMutationResponse is an HTTP/1.1 upstream SSE response streaming body.
func sseMutationResponse(body io.Reader) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{sseContentType}},
		Body:          io.NopCloser(body),
		ContentLength: -1,
	}
}

// In an SSE body only the data: line of the matching response changes;
// every other byte, the field's optional space and the line terminators
// pass through as sent. A line longer than the reader's 4 KiB buffer is
// joined from several reads before it is checked; a line too long to
// inspect passes through whole.
func TestMutateMCPResponseSSE(t *testing.T) {
	const (
		progress = `data: {"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`
		response = `{"jsonrpc":"2.0","id":"c1","result":` + mcpResult + `}`
		mutated  = `{"id":"c1","jsonrpc":"2.0","result":` + mcpMutated + `}`
	)
	tooLong := `data: ` + rpcResponse(`"c1"`, `{"structuredContent":{"blob":"`+strings.Repeat("x", maxMutatedBody)+`"}}`) + "\n\n"
	longResult := `{"structuredContent":{"blob":"` + strings.Repeat("x", 10<<10) + `","n":1}}`
	cases := []struct {
		name, body, want string
	}{
		{
			name: "10 KiB line, joined from several reads",
			body: progress + "\n\ndata: " + rpcResponse(`"c1"`, longResult) + "\n\n",
			want: progress + "\n\ndata: " + `{"id":"c1","jsonrpc":"2.0","result":{"structuredContent":{"blob":true,"n":"1"}}}` + "\n\n",
		},
		{name: "10 KiB line of another id", body: "data: " + rpcResponse(`"c2"`, longResult) + "\n\n"},
		{
			name: "LF, notification first",
			body: "event: message\n" + progress + "\n\nevent: message\ndata: " + response + "\n\n",
			want: "event: message\n" + progress + "\n\nevent: message\ndata: " + mutated + "\n\n",
		},
		{
			name: "CRLF, no space after the colon",
			body: "id: 1\r\nevent: message\r\ndata:" + response + "\r\n\r\n",
			want: "id: 1\r\nevent: message\r\ndata:" + mutated + "\r\n\r\n",
		},
		{
			name: "only the first matching response",
			body: "data: " + response + "\n\ndata: " + response + "\n\n",
			want: "data: " + mutated + "\n\ndata: " + response + "\n\n",
		},
		{
			name: "last line without a terminator",
			body: "data: " + response,
			want: "data: " + mutated,
		},
		{name: "no matching response", body: progress + "\n\n" + "data: " + rpcResponse(`"c2"`, mcpResult) + "\n\n"},
		{name: "line over the limit", body: tooLong},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.want
			if want == "" {
				want = c.body
			}
			var calls atomic.Int32
			res := sseMutationResponse(strings.NewReader(c.body))
			mutateMCPResponse(res, "c1", func() { calls.Add(1) })
			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if string(got) != want {
				t.Errorf("client got %d bytes, want %d; tail %q, want tail %q", len(got), len(want), tail(got), tail([]byte(want)))
			}
			wantCalls := int32(0)
			if c.want != "" {
				wantCalls = 1
			}
			if n := calls.Load(); n != wantCalls {
				t.Errorf("onMutate calls = %d, want %d", n, wantCalls)
			}
		})
	}
}

// onMutate runs before the client can read the mutated line, so the CLI
// can't stop the injector, and seal the event out, on a target that
// reacted to the mismatch. The sleep gives a client that could read early
// the time to do so.
func TestSSEMutationEmitsBeforeTheClientSeesIt(t *testing.T) {
	res := sseMutationResponse(strings.NewReader("data: " + rpcResponse(`1`, `{"structuredContent":{"n":1}}`) + "\n\n"))
	var emitted atomic.Bool
	mutateMCPResponse(res, mustRPCID(t, `1`), func() {
		time.Sleep(10 * time.Millisecond)
		emitted.Store(true)
	})
	if _, err := io.ReadFull(res.Body, make([]byte, 1)); err != nil {
		t.Fatalf("read the first byte: %v", err)
	}
	if !emitted.Load() {
		t.Error("the client read the mutated line before onMutate returned")
	}
	if _, err := io.ReadAll(res.Body); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
}

// The wrapped body returns an upstream read error, as the upstream body
// would without the fault, rather than a clean end of the stream, and a
// mutation that didn't happen isn't reported.
func TestSSEMutationUpstreamError(t *testing.T) {
	upstreamErr := errors.New("upstream reset")
	const sent = "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n"
	res := sseMutationResponse(&errAfterReader{r: strings.NewReader(sent), err: upstreamErr})
	var calls atomic.Int32
	mutateMCPResponse(res, mustRPCID(t, `1`), func() { calls.Add(1) })
	got, err := io.ReadAll(res.Body)
	if !errors.Is(err, upstreamErr) {
		t.Errorf("wrapped body read error = %v, want %v", err, upstreamErr)
	}
	if string(got) != sent {
		t.Errorf("wrapped body gave %q, want %q before the error", got, sent)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("onMutate calls = %d, want 0", n)
	}
}

// Closing the wrapped body, as a client that goes away does, closes the
// upstream and ends the forwarding goroutine.
func TestSSEMutationClosesUpstreamOnDownstreamClose(t *testing.T) {
	up := newBlockingUpstream()
	res := &http.Response{Header: http.Header{"Content-Type": []string{sseContentType}}, Body: up}
	mutateMCPResponse(res, mustRPCID(t, `1`), func() { t.Error("onMutate on an idle stream") })
	if err := res.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !up.closeCalled.Load() {
		t.Error("upstream not closed after the downstream closed")
	}
}

// The mutated stream has no length, so it must end some other way: chunked
// over HTTP/1.1, or by closing the connection over HTTP/1.0.
func TestSSEMutationFraming(t *testing.T) {
	cases := []struct {
		name         string
		major, minor int
		wantChunked  bool
	}{
		{name: "HTTP/1.1", major: 1, minor: 1, wantChunked: true},
		{name: "HTTP/1.0", major: 1, minor: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			const body = "data: {}\n\n"
			res := sseMutationResponse(strings.NewReader(body))
			res.ProtoMajor, res.ProtoMinor = c.major, c.minor
			res.Header.Set("Content-Length", strconv.Itoa(len(body)))
			res.ContentLength = int64(len(body))
			mutateMCPResponse(res, mustRPCID(t, `1`), func() {})
			if _, err := io.ReadAll(res.Body); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if res.ContentLength != -1 || res.Header.Get("Content-Length") != "" {
				t.Errorf("ContentLength %d, header %q; want the length dropped", res.ContentLength, res.Header.Get("Content-Length"))
			}
			chunked := len(res.TransferEncoding) == 1 && res.TransferEncoding[0] == "chunked"
			if chunked != c.wantChunked || res.Close == c.wantChunked {
				t.Errorf("TransferEncoding %v, Close %v; want chunked %v, Close %v", res.TransferEncoding, res.Close, c.wantChunked, !c.wantChunked)
			}
		})
	}
}
