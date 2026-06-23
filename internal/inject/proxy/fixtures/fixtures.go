// Package fixtures builds vendor-accurate synthetic HTTP responses
// for the proxy injector. Dispatch is by host: api.openai.com gets
// OpenAI-shaped error bodies; api.anthropic.com gets Anthropic-shaped
// ones; other hosts get a generic shape.
package fixtures

import (
	"fmt"
	"net/http"

	"github.com/faultkit/faultkit/pkg/faulttypes"
)

// Synthetic carries a fully-formed synthetic HTTP response.
type Synthetic struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// Build returns a Synthetic for the given host and fault. If
// fault.ResponseBody is set, it is returned verbatim; otherwise the
// body is synthesized from a per-vendor template.
func Build(host string, fault faulttypes.Fault) Synthetic {
	switch host {
	case "api.openai.com":
		return vendorResponse(fault, openAIErrorBody)
	case "api.anthropic.com":
		return vendorResponse(fault, anthropicErrorBody)
	default:
		return vendorResponse(fault, genericErrorBody)
	}
}

// genericErrorBody is the vendor-agnostic error shape used for hosts
// faultkit has no specific template for.
func genericErrorBody(status int) []byte {
	return []byte(fmt.Sprintf(`{"error":{"message":%q,"type":"api_error"}}`, http.StatusText(status)))
}

// vendorResponse is the shared shape: caller-supplied body verbatim
// when ResponseBody is set; otherwise a per-vendor error body for 4xx
// and 5xx; an empty JSON object for 2xx/3xx.
func vendorResponse(fault faulttypes.Fault, errorBody func(int) []byte) Synthetic {
	status := defaultStatus(fault)
	headers := mergeHeaders(fault, "application/json")

	if fault.ResponseBody != "" {
		return Synthetic{Status: status, Headers: headers, Body: []byte(fault.ResponseBody)}
	}
	if status >= 400 {
		return Synthetic{Status: status, Headers: headers, Body: errorBody(status)}
	}
	return Synthetic{Status: status, Headers: headers, Body: []byte("{}")}
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
