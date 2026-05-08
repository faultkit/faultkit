package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
)

const sseContentType = "text/event-stream"

// wrapStreamCutoff replaces res.Body with a reader that forwards the
// upstream stream line-by-line until cutAt SSE `data:` events have
// been seen, then closes the connection without emitting the
// `[DONE]` sentinel. No-op for non-SSE responses.
func wrapStreamCutoff(res *http.Response, cutAt int) {
	if !isSSE(res) {
		return
	}
	upstream := res.Body
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		defer upstream.Close()

		scanner := bufio.NewScanner(upstream)
		scanner.Buffer(make([]byte, 4096), 1024*1024)

		eventCount := 0
		for scanner.Scan() {
			line := scanner.Bytes()
			if _, err := pw.Write(line); err != nil {
				return
			}
			if _, err := pw.Write([]byte("\n")); err != nil {
				return
			}
			if bytes.HasPrefix(line, []byte("data:")) {
				eventCount++
				if eventCount >= cutAt {
					return
				}
			}
		}
	}()

	// Closing res.Body downstream must propagate to upstream so the
	// goroutine unblocks and the upstream connection is released.
	res.Body = &streamBody{ReadCloser: pr, upstream: upstream}
	res.Header.Del("Content-Length")
	res.ContentLength = -1
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
