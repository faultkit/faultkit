package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// maxMutatedBody is the most faultkit reads of a JSON-RPC request body, a
// JSON response body or one SSE line to apply a response mutation, the
// same limit as maxClassifiedLine. Anything longer passes through
// unchanged and unmutated.
const maxMutatedBody = 1 << 20

// ctxKeyRPCID holds the JSON-RPC id of a tools/call request whose response
// a mutation fault edits.
const ctxKeyRPCID = "faultkit.rpc-id"

// replayBody is a body read for inspection and put back: it yields the
// bytes that were read, then the rest of the original, whose Close it
// keeps.
type replayBody struct {
	io.Reader
	io.Closer
}

// readBounded reads up to maxMutatedBody bytes of rc. It returns them,
// whether they were all of rc, and a body that yields the same bytes. If
// the read failed, the replay yields the bytes read and then reads rc
// again: a chunked body fails again, while a net/http body with a known
// length that came up short now ends cleanly. The Transport still refuses
// to forward that one, because it is shorter than its Content-Length.
func readBounded(rc io.ReadCloser) ([]byte, bool, io.ReadCloser) {
	data, err := io.ReadAll(io.LimitReader(rc, maxMutatedBody+1))
	if err != nil || len(data) > maxMutatedBody {
		return nil, false, replayBody{Reader: io.MultiReader(bytes.NewReader(data), rc), Closer: rc}
	}
	return data, true, replayBody{Reader: bytes.NewReader(data), Closer: rc}
}

// toolsCallID reads req's body, up to maxMutatedBody, and puts it back
// unchanged. It returns the JSON-RPC id if the body is a single MCP
// tools/call request. A batch, a larger body or anything else returns
// false.
func toolsCallID(req *http.Request) (any, bool) {
	if req.Body == nil || req.Body == http.NoBody || req.ContentLength > maxMutatedBody {
		return nil, false
	}
	data, whole, body := readBounded(req.Body)
	req.Body = body
	if !whole {
		return nil, false
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(data, &msg) != nil {
		return nil, false
	}
	var method string
	if json.Unmarshal(msg["method"], &method) != nil || method != "tools/call" {
		return nil, false
	}
	return rpcID(msg["id"])
}

// numericID is a JSON-RPC id that is a number, in a canonical form: its
// sign, its significant digits and a base-10 exponent. Two numeric ids are
// equal exactly when their values are, so 7, 7.0 and 0.7e1 are one id,
// while 9007199254740993 and 9007199254740992 are two. Its own type keeps
// it from equalling the string id "7".
type numericID string

// rpcID decodes a JSON-RPC id. Only a string or a number is a request id;
// a number becomes its numericID.
func rpcID(raw json.RawMessage) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var id any
	if dec.Decode(&id) != nil {
		return nil, false
	}
	switch v := id.(type) {
	case string:
		return v, true
	case json.Number:
		return canonicalNumber(v.String())
	}
	return nil, false
}

// canonicalNumber returns the numericID of n, the text of a valid JSON
// number. It reports false for an exponent of more than 8 digits, which no
// real id has, so the exponent arithmetic can't overflow.
func canonicalNumber(n string) (any, bool) {
	sign := ""
	if strings.HasPrefix(n, "-") {
		sign, n = "-", n[1:]
	}
	exp := 0
	if i := strings.IndexAny(n, "eE"); i >= 0 {
		e := n[i+1:]
		if len(strings.TrimLeft(strings.TrimLeft(e, "+-"), "0")) > 8 {
			return nil, false
		}
		var err error
		if exp, err = strconv.Atoi(e); err != nil {
			return nil, false
		}
		n = n[:i]
	}
	whole, frac, _ := strings.Cut(n, ".")
	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		return numericID("0"), true // -0 is 0
	}
	significant := strings.TrimRight(digits, "0")
	exp += len(digits) - len(significant) - len(frac)
	return numericID(sign + significant + "e" + strconv.Itoa(exp)), true
}

// mutateMCPResponse applies mcp-schema-mismatch to res, the response to the
// tools/call request with JSON-RPC id id: in the JSON-RPC response to that
// id, every top-level value of result.structuredContent changes type. It
// handles an application/json body of at most maxMutatedBody bytes, and
// each data: line of a text/event-stream body. Anything else, including a
// compressed body, passes through unchanged.
//
// onMutate runs once, after the mutation and before the client can read
// the mutated message, so a target that exits on the mismatch can't end
// the run before the event is sent. A response with no matching message,
// or one without structuredContent, never calls it.
func mutateMCPResponse(res *http.Response, id any, onMutate func()) {
	if ce := res.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return
	}
	switch {
	case isSSE(res):
		wrapSSEMutation(res, id, onMutate)
	case isJSON(res):
		mutateJSONBody(res, id, onMutate)
	}
}

func isJSON(res *http.Response) bool {
	mediaType, _, _ := strings.Cut(res.Header.Get("Content-Type"), ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

// mutateJSONBody mutates a JSON response body that holds the response to
// id, and fixes its length.
func mutateJSONBody(res *http.Response, id any, onMutate func()) {
	if res.ContentLength > maxMutatedBody {
		return
	}
	data, whole, body := readBounded(res.Body)
	res.Body = body
	if !whole {
		return
	}
	out, ok := mutateToolResult(data, id)
	if !ok {
		return
	}
	onMutate()
	res.Body = replayBody{Reader: bytes.NewReader(out), Closer: body}
	res.ContentLength = int64(len(out))
	res.Header.Set("Content-Length", strconv.Itoa(len(out)))
	res.TransferEncoding = nil
}

// wrapSSEMutation replaces res.Body with a reader that forwards the
// upstream SSE stream a line at a time and mutates the data: line that
// holds the response to id, like wrapStreamCutoff's forwarder. Every other
// byte passes through unchanged. An upstream read error ends the wrapped
// body with that error, as it would end the unwrapped one; through martian
// the client then waits for its own timeout either way.
func wrapSSEMutation(res *http.Response, id any, onMutate func()) {
	upstream := res.Body
	pr, pw := io.Pipe()

	go func() {
		defer upstream.Close()
		pw.CloseWithError(forwardMutatingSSE(pw, upstream, id, onMutate))
	}()

	res.Body = &streamBody{ReadCloser: pr, upstream: upstream}
	// The mutated stream's length isn't known up front. Without a length,
	// chunks or a closing connection, the client would wait for more after
	// the stream ends, because martian keeps the connection open. So it is
	// sent in chunks, as most streams already are, or, over HTTP/1.0, which
	// has no chunks, ends with the connection.
	res.Header.Del("Content-Length")
	res.ContentLength = -1
	if res.ProtoAtLeast(1, 1) {
		res.TransferEncoding = []string{"chunked"}
	} else {
		res.Close = true
	}
}

// forwardMutatingSSE copies src to dst, holding each line back until it is
// complete. The first data: line that mutateSSELine changes is written
// mutated, after onMutate; the rest of the stream is then copied as is. A
// line longer than maxMutatedBody is forwarded unchanged in chunks. It
// returns the error that ended the copy, or nil at the end of src.
func forwardMutatingSSE(dst io.Writer, src io.Reader, id any, onMutate func()) error {
	r := bufio.NewReader(src)
	var line []byte  // the start of a line longer than r's buffer
	tooLong := false // the current line exceeds maxMutatedBody and is being forwarded
	for {
		chunk, err := r.ReadSlice('\n')
		partial := errors.Is(err, bufio.ErrBufferFull)
		switch {
		case tooLong:
			if werr := write(dst, chunk); werr != nil {
				return werr
			}
		case len(line)+len(chunk) > maxMutatedBody:
			if werr := write(dst, line, chunk); werr != nil {
				return werr
			}
			line, tooLong = line[:0], true
		case partial:
			line = append(line, chunk...)
		default:
			whole := chunk
			if len(line) > 0 {
				line = append(line, chunk...)
				whole = line
			}
			out, mutated := mutateSSELine(whole, id)
			if mutated {
				onMutate()
			} else {
				out = whole
			}
			if werr := write(dst, out); werr != nil {
				return werr
			}
			if mutated && err == nil {
				// One response per request id: the rest needs no look.
				_, cerr := io.Copy(dst, r)
				return cerr
			}
			line = line[:0]
		}
		if partial {
			continue
		}
		tooLong = false
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// write writes each non-empty b to dst. An empty write would still wait
// for the pipe's reader.
func write(dst io.Writer, bs ...[]byte) error {
	for _, b := range bs {
		if len(b) == 0 {
			continue
		}
		if _, err := dst.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// mutateSSELine returns line with its data: payload mutated by
// mutateToolResult, keeping the field name, the optional space after it
// and the line terminator byte for byte.
func mutateSSELine(line []byte, id any) ([]byte, bool) {
	// HasPrefix implies the length check; nilaway needs it spelled out to
	// see that line isn't nil.
	if len(line) == 0 || !bytes.HasPrefix(line, sseDataPrefix) {
		return nil, false
	}
	head := len(sseDataPrefix)
	if head < len(line) && line[head] == ' ' {
		head++
	}
	end := len(line)
	switch {
	case bytes.HasSuffix(line, []byte("\r\n")):
		end -= 2
	case bytes.HasSuffix(line, []byte("\n")):
		end--
	}
	out, ok := mutateToolResult(line[head:end], id)
	if !ok {
		return nil, false
	}
	mutated := make([]byte, 0, head+len(out)+len(line)-end)
	mutated = append(mutated, line[:head]...)
	mutated = append(mutated, out...)
	return append(mutated, line[end:]...), true
}

// mutateToolResult changes the type of every top-level value of
// result.structuredContent in msg, if msg is the JSON-RPC response to id.
// The id and every other member are kept; the message is re-encoded
// compactly, with object members in sorted order. It returns false, and
// msg stays as it is, if msg isn't that response, has no structuredContent
// object or that object is empty.
func mutateToolResult(msg []byte, id any) ([]byte, bool) {
	// null decodes to a nil map without an error.
	var m map[string]json.RawMessage
	if json.Unmarshal(msg, &m) != nil || m == nil {
		return nil, false
	}
	if got, ok := rpcID(m["id"]); !ok || got != id {
		return nil, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(m["result"], &result) != nil || result == nil {
		return nil, false
	}
	var sc map[string]json.RawMessage
	if json.Unmarshal(result["structuredContent"], &sc) != nil || len(sc) == 0 {
		return nil, false
	}
	for k, v := range sc {
		changed, err := changeType(v)
		if err != nil {
			return nil, false
		}
		sc[k] = changed
	}
	var err error
	if result["structuredContent"], err = compactJSON(sc); err != nil {
		return nil, false
	}
	if m["result"], err = compactJSON(result); err != nil {
		return nil, false
	}
	out, err := compactJSON(m)
	if err != nil {
		return nil, false
	}
	return out, true
}

// changeType returns v as a value of another JSON type. A string becomes
// the number it holds ("42" → 42), or else true. Any other value becomes
// its own compact JSON text as a string: 42 → "42", true → "true",
// null → "null", {"a":1} → "{\"a\":1}".
func changeType(v json.RawMessage) (json.RawMessage, error) {
	if v = bytes.TrimSpace(v); len(v) > 0 && v[0] == '"' {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, err
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
			return compactJSON(n)
		}
		return json.RawMessage("true"), nil
	}
	var text bytes.Buffer
	if err := json.Compact(&text, v); err != nil {
		return nil, err
	}
	return compactJSON(text.String())
}

// compactJSON encodes v without HTML escaping, so the strings and raw
// members carried over from upstream keep their characters.
func compactJSON(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
