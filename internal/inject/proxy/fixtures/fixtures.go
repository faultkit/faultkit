// Package fixtures builds vendor-accurate synthetic HTTP responses
// for the proxy injector. Dispatch is by host: api.openai.com gets
// OpenAI-shaped error bodies; other hosts get a generic shape.
package fixtures

import (
	"fmt"
	"net/http"

	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
)

// Synthetic carries a fully-formed synthetic HTTP response.
type Synthetic struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// Build returns a Synthetic for the given host and fault. If
// fault.ResponseBody is set, it is returned verbatim; otherwise the
// body is synthesized from a per-vendor template (OpenAI for
// api.openai.com, generic shape for everything else).
func Build(host string, fault faulttypes.Fault) Synthetic {
	if matchesOpenAI(host) {
		return openAIResponse(fault)
	}
	return genericResponse(fault)
}

func matchesOpenAI(host string) bool {
	return host == "api.openai.com"
}

func defaultStatus(fault faulttypes.Fault) int {
	if fault.HTTPStatus != 0 {
		return fault.HTTPStatus
	}
	return http.StatusOK
}

func mergeHeaders(fault faulttypes.Fault, contentType string) map[string]string {
	out := make(map[string]string, len(fault.ResponseHeaders)+1)
	for k, v := range fault.ResponseHeaders {
		out[k] = v
	}
	if _, ok := out["Content-Type"]; !ok {
		out["Content-Type"] = contentType
	}
	return out
}

func genericResponse(fault faulttypes.Fault) Synthetic {
	status := defaultStatus(fault)
	headers := mergeHeaders(fault, "application/json")
	if fault.ResponseBody != "" {
		return Synthetic{Status: status, Headers: headers, Body: []byte(fault.ResponseBody)}
	}
	if status >= 400 {
		body := []byte(fmt.Sprintf(`{"error":{"message":%q,"type":"api_error"}}`, http.StatusText(status)))
		return Synthetic{Status: status, Headers: headers, Body: body}
	}
	return Synthetic{Status: status, Headers: headers, Body: []byte("{}")}
}
