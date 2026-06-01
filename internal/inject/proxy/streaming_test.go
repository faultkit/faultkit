package proxy

import (
	"io"
	"net/http"
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
