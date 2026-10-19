package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
)

const sseContentType = "text/event-stream"

// maxClassifiedLine is the longest SSE line, terminator included, that is
// checked for a content delta. A longer line is still forwarded, in
// reader-sized chunks, but never counts toward the cut.
const maxClassifiedLine = 1 << 20

// wrapStreamCutoff replaces res.Body with a reader that forwards the
// upstream stream line-by-line until cutAt content deltas have been
// seen, then closes the connection without emitting the `[DONE]`
// sentinel. Lifecycle events (message_start, ping, content_block_start)
// pass through but do not consume the budget. No-op for non-SSE responses.
// Bytes before the cut are forwarded unchanged; a line longer than
// maxClassifiedLine passes through too, so faultkit never ends a stream
// because of its own limit.
//
// onCut runs once, on the forwarding goroutine, right before the cut, so
// the fault counts as applied only when the stream was actually cut. A
// stream that ends with fewer than cutAt content deltas passes through
// whole and never calls it. An upstream read error before the cut goes to
// onErr instead, so a broken upstream looks different from a clean cut.
// Either callback may be nil.
//
// A client that goes away before the cut is noticed only when a write to
// it fails. In base-URL mode that ends the stream before the cut, usually
// with an onErr event. In forward-proxy mode martian buffers writes to the
// client, so the stream can still reach the cut and call onCut.
//
// The goroutine exits cooperatively when downstream Close propagates
// through streamBody → io.Pipe → pw.Write returns io.ErrClosedPipe.
// This is the contract that lets us avoid an extra signal channel; if
// the streamBody wrapper is removed, the goroutine becomes a leak.
func wrapStreamCutoff(res *http.Response, cutAt int, onCut func(), onErr func(error)) {
	if !isSSE(res) {
		return
	}
	upstream := res.Body
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		defer upstream.Close()

		cut, err := forwardUntilCut(pw, upstream, cutAt)
		switch {
		case cut && onCut != nil:
			onCut()
		case err != nil && onErr != nil:
			onErr(err)
		}
	}()

	res.Body = &streamBody{ReadCloser: pr, upstream: upstream}
	res.Header.Del("Content-Length")
	res.ContentLength = -1
}

// forwardUntilCut copies src to dst unchanged, a line at a time, and stops
// after the line that holds the cutAt-th content delta. It reports whether
// it cut, or the upstream read error other than io.EOF that ended the
// stream. A failed write to dst (the client went away) returns neither.
func forwardUntilCut(dst io.Writer, src io.Reader, cutAt int) (bool, error) {
	r := bufio.NewReader(src)
	var long []byte  // a line longer than r's buffer, collected to classify it
	tooLong := false // the current line exceeds maxClassifiedLine
	deltas := 0
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			if _, werr := dst.Write(chunk); werr != nil {
				return false, nil
			}
		}

		line := chunk
		partial := errors.Is(err, bufio.ErrBufferFull)
		if partial || len(long) > 0 || tooLong {
			if !tooLong && len(long)+len(chunk) <= maxClassifiedLine {
				long = append(long, chunk...)
			} else {
				tooLong, long = true, long[:0]
			}
			if partial {
				continue
			}
			line = long
		}
		counted := !tooLong && isContentDelta(line)
		long, tooLong = long[:0], false

		if counted {
			deltas++
			if deltas >= cutAt {
				return true, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
	}
}

type streamBody struct {
	io.ReadCloser
	upstream io.Closer
}

func (s *streamBody) Close() error {
	err := s.ReadCloser.Close()
	_ = s.upstream.Close()
	return err
}

func isSSE(res *http.Response) bool {
	return strings.HasPrefix(res.Header.Get("Content-Type"), sseContentType)
}

// SSE content-delta sentinels, hoisted to package level so isContentDelta
// doesn't allocate a []byte per line in the streaming-cutoff goroutine.
var (
	sseDataPrefix       = []byte("data:")
	sseAnthropicDelta   = []byte("content_block_delta")
	sseOpenAIDeltaKey   = []byte(`"delta"`)
	sseOpenAIContentKey = []byte(`"content"`)
)

// isContentDelta reports whether an SSE line carries model content (a token-ish
// delta), as opposed to lifecycle events (message_start, ping, content_block_start).
// ponytail: substring heuristic over the two provider shapes we ship — OpenAI
// (choices[].delta.content) and Anthropic (content_block_delta). Refine to a
// JSON parse only if a provider's shape starts to alias.
func isContentDelta(line []byte) bool {
	if !bytes.HasPrefix(line, sseDataPrefix) {
		return false
	}
	return bytes.Contains(line, sseAnthropicDelta) || // Anthropic
		(bytes.Contains(line, sseOpenAIDeltaKey) && bytes.Contains(line, sseOpenAIContentKey)) // OpenAI
}
