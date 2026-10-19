package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	wrapStreamCutoff(res, 1000, nil, nil)

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
	wrapStreamCutoff(res, 3, nil, nil)
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
	wrapStreamCutoff(res, 2, nil, nil)
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
	wrapStreamCutoff(res, 2, nil, nil)
	got, _ := io.ReadAll(res.Body)
	if !bytes.Contains(got, []byte(`"content":"b"`)) {
		t.Errorf("cut too early: %q", got)
	}
	if bytes.Contains(got, []byte(`"content":"c"`)) {
		t.Errorf("cut too late — 3rd content delta leaked: %q", got)
	}
}

// sseDone is the OpenAI end-of-stream event.
const sseDone = "data: [DONE]\n\n"

// openAIDeltas returns an OpenAI-shape SSE stream of n content deltas
// followed by the [DONE] sentinel.
func openAIDeltas(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
	}
	b.WriteString(sseDone)
	return b.String()
}

// cutAtLast is what the client receives when the cut lands on the last
// delta of openAIDeltas(n): everything up to and including that line.
func cutAtLast(n int) string {
	return strings.TrimSuffix(openAIDeltas(n), "\n"+sseDone)
}

// deltaLine returns an OpenAI content-delta line of exactly size bytes,
// its "\n" included.
func deltaLine(size int) string {
	const open, closing = `data: {"choices":[{"delta":{"content":"`, `"}}]}` + "\n"
	return open + strings.Repeat("x", size-len(open)-len(closing)) + closing
}

// onCut runs exactly once when the stream reaches the cut point, and never
// when the stream ends first. The client gets the upstream bytes unchanged
// up to the cut, or all of them when there is no cut. A line longer than
// maxClassifiedLine passes through whole and doesn't count. onCut runs
// before the downstream sees EOF (see TestStreamCutoffOnCutBeforeEOF), so
// ReadAll returning is enough to check it.
func TestStreamCutoffOnCutOnlyWhenCut(t *testing.T) {
	const cutAt = 3
	atLimit := deltaLine(maxClassifiedLine) + "\n"
	overLimit := deltaLine(maxClassifiedLine+1) + "\n"
	cases := []struct {
		name     string
		body     string
		want     string
		wantCuts int32
	}{
		{name: "fewer deltas than the cut point", body: openAIDeltas(cutAt - 1), want: openAIDeltas(cutAt - 1)},
		{name: "exactly the cut point", body: openAIDeltas(cutAt), want: cutAtLast(cutAt), wantCuts: 1},
		{name: "more deltas than the cut point", body: openAIDeltas(cutAt + 5), want: cutAtLast(cutAt), wantCuts: 1},
		{
			name: "CRLF line endings pass through",
			body: strings.ReplaceAll(openAIDeltas(cutAt-1), "\n", "\r\n"),
			want: strings.ReplaceAll(openAIDeltas(cutAt-1), "\n", "\r\n"),
		},
		{
			name:     "delta line at the classify limit counts",
			body:     atLimit + openAIDeltas(cutAt-1),
			want:     atLimit + cutAtLast(cutAt-1),
			wantCuts: 1,
		},
		{
			name: "oversized line before too few deltas passes through",
			body: overLimit + openAIDeltas(cutAt-1),
			want: overLimit + openAIDeltas(cutAt-1),
		},
		{
			name:     "oversized line then enough deltas cuts at the cut point",
			body:     overLimit + openAIDeltas(cutAt),
			want:     overLimit + cutAtLast(cutAt),
			wantCuts: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cuts atomic.Int32
			res := sseResponse(tc.body)
			wrapStreamCutoff(res, cutAt, func() { cuts.Add(1) }, func(err error) {
				t.Errorf("onErr(%v) on a clean stream", err)
			})
			got, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if n := cuts.Load(); n != tc.wantCuts {
				t.Errorf("onCut calls = %d, want %d", n, tc.wantCuts)
			}
			if string(got) != tc.want {
				t.Errorf("client got %d bytes, want %d; tail %q, want tail %q",
					len(got), len(tc.want), tail(got), tail([]byte(tc.want)))
			}
		})
	}
}

// tail returns the last bytes of b, for readable failure messages.
func tail(b []byte) []byte {
	return b[max(0, len(b)-80):]
}

// onCut runs before the downstream can see the end of the body. The CLI
// stops the injector once the target exits, so a fired event sent after
// EOF could be sealed out and the run would wrongly exit 3.
func TestStreamCutoffOnCutBeforeEOF(t *testing.T) {
	res := sseResponse(openAIDeltas(5))
	read, cutDone := make(chan struct{}), make(chan struct{})
	wrapStreamCutoff(res, 3, func() {
		defer close(cutDone)
		select {
		case <-read:
			t.Error("downstream reached the end of the body before onCut ran")
		case <-time.After(10 * time.Millisecond):
		}
	}, nil)
	if _, err := io.ReadAll(res.Body); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	close(read)
	select {
	case <-cutDone:
	case <-time.After(5 * time.Second):
		t.Fatal("onCut never ran")
	}
}

// errAfterReader serves data, then fails with err instead of io.EOF.
type errAfterReader struct {
	r   io.Reader
	err error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		return n, e.err
	}
	return n, err
}

// An upstream read error before the cut point goes to onErr, not onCut.
func TestStreamCutoffUpstreamErrorBeforeCut(t *testing.T) {
	upstreamErr := errors.New("upstream reset")
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{sseContentType}},
		Body: io.NopCloser(&errAfterReader{
			r:   strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"),
			err: upstreamErr,
		}),
	}
	var cuts atomic.Int32
	var gotErr error
	wrapStreamCutoff(res, 3, func() { cuts.Add(1) }, func(err error) { gotErr = err })
	if _, err := io.ReadAll(res.Body); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if n := cuts.Load(); n != 0 {
		t.Errorf("onCut calls = %d, want 0 when the upstream fails first", n)
	}
	if !errors.Is(gotErr, upstreamErr) {
		t.Errorf("onErr got %v, want %v", gotErr, upstreamErr)
	}
}
