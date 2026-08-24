package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// blockingUpstream simulates an idle upstream SSE body: Read blocks until
// Close is called, mimicking a streaming connection that hasn't sent data
// yet. It records whether Close ran. A real net.Conn behaves the same way —
// Close unblocks an in-flight Read.
type blockingUpstream struct {
	unblock     chan struct{}
	once        sync.Once
	closeCalled atomic.Bool
}

func newBlockingUpstream() *blockingUpstream {
	return &blockingUpstream{unblock: make(chan struct{})}
}

func (b *blockingUpstream) Read([]byte) (int, error) {
	<-b.unblock
	return 0, io.EOF
}

func (b *blockingUpstream) Close() error {
	b.closeCalled.Store(true)
	b.once.Do(func() { close(b.unblock) })
	return nil
}

// TestWrapStreamCutoffClosesUpstreamOnDownstreamClose locks in the
// goroutine-leak contract documented in streaming.go: closing the wrapped
// response body must close the upstream, which unblocks the forwarding
// goroutine's Read so it exits instead of leaking. If the streamBody
// wrapper that bridges downstream Close to upstream Close is ever removed,
// this test fails.
func TestWrapStreamCutoffClosesUpstreamOnDownstreamClose(t *testing.T) {
	up := newBlockingUpstream()
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{sseContentType}},
		Body:   up,
	}

	// High cut threshold so the goroutine never cuts on its own; we model
	// the client disconnecting early instead.
	wrapStreamCutoff(res, 1000, nil)

	if err := res.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !up.closeCalled.Load() {
		t.Fatal("closing the response body must close the upstream, else the forwarding goroutine leaks")
	}
}

// TestWrapStreamCutoffIgnoresNonSSE verifies the wrapper is a no-op for
// non-streaming responses, so it can't break the synchronous proxy
// scenarios (llm-api-degraded, malformed-json-response).
func TestWrapStreamCutoffIgnoresNonSSE(t *testing.T) {
	up := newBlockingUpstream()
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   up,
	}
	wrapStreamCutoff(res, 3, nil)
	if _, wrapped := res.Body.(*streamBody); wrapped {
		t.Error("non-SSE response body should be left untouched, not wrapped")
	}
}

// sseResponse returns a minimal *http.Response whose body is an SSE stream
// backed by the given string. Used by content-delta counting tests.
func sseResponse(body string) *http.Response {
	return &http.Response{
		Header: http.Header{"Content-Type": []string{sseContentType}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

// TestStreamCutoffCountsContentDeltasOnly verifies that lifecycle events
// (message_start, ping, content_block_start) do not consume the cutoff
// budget — only content-bearing deltas count.
func TestStreamCutoffCountsContentDeltasOnly(t *testing.T) {
	// One message_start (data:, not content) then 3 content deltas.
	// Cut at 2 must forward through the 2nd content delta, not stop at message_start.
	upstream := "event: message_start\n" +
		`data: {"type":"message_start"}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"text":"a"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"text":"b"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"text":"c"}}` + "\n\n"

	res := sseResponse(upstream)
	wrapStreamCutoff(res, 2, nil)
	got, _ := io.ReadAll(res.Body)

	// message_start passes through; content deltas a and b pass; c is cut.
	if !bytes.Contains(got, []byte(`"message_start"`)) {
		t.Errorf("lifecycle event was dropped, not just budget-neutral: %q", got)
	}
	if !bytes.Contains(got, []byte(`"text":"b"`)) {
		t.Errorf("cut too early: %q", got)
	}
	if bytes.Contains(got, []byte(`"text":"c"`)) {
		t.Errorf("cut too late — 3rd content delta leaked: %q", got)
	}
}

// TestStreamCutoffOpenAIContentDeltas verifies the OpenAI shape: a leading
// role-only delta (no "content" key) must NOT consume the budget.
func TestStreamCutoffOpenAIContentDeltas(t *testing.T) {
	// role-only delta: "delta" present but no "content" key — must not count.
	upstream := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"c\"}}]}\n\n"
	res := sseResponse(upstream)
	wrapStreamCutoff(res, 2, nil)
	got, _ := io.ReadAll(res.Body)
	if !bytes.Contains(got, []byte(`"content":"b"`)) {
		t.Errorf("cut too early: %q", got)
	}
	if bytes.Contains(got, []byte(`"content":"c"`)) {
		t.Errorf("cut too late — 3rd content delta leaked: %q", got)
	}
}
