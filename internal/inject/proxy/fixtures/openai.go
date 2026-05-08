package fixtures

import (
	"encoding/json"
	"net/http"

	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
)

func openAIResponse(fault faulttypes.Fault) Synthetic {
	status := defaultStatus(fault)
	headers := mergeHeaders(fault, "application/json")

	if fault.ResponseBody != "" {
		return Synthetic{Status: status, Headers: headers, Body: []byte(fault.ResponseBody)}
	}

	if status >= 400 {
		return Synthetic{Status: status, Headers: headers, Body: openAIErrorBody(status)}
	}

	return Synthetic{Status: status, Headers: headers, Body: []byte("{}")}
}

// openAIErrorBody returns the OpenAI error envelope for status. The
// four common statuses are precomputed; uncommon statuses fall back
// to a dynamically-marshaled body.
func openAIErrorBody(status int) []byte {
	if b, ok := openAIErrorBodies[status]; ok {
		return b
	}
	return marshalOpenAIError(http.StatusText(status), "api_error", "")
}

var openAIErrorBodies = map[int][]byte{
	http.StatusTooManyRequests:     marshalOpenAIError("Rate limit reached for requests", "rate_limit_error", "rate_limit_exceeded"),
	http.StatusServiceUnavailable:  marshalOpenAIError("The server is temporarily overloaded or under maintenance.", "server_error", "service_unavailable"),
	http.StatusGatewayTimeout:      marshalOpenAIError("The server timed out processing the request.", "server_error", "timeout"),
	http.StatusInternalServerError: marshalOpenAIError("The server had an error while processing your request.", "server_error", "internal_error"),
}

func marshalOpenAIError(msg, errType, code string) []byte {
	payload := map[string]any{
		"error": map[string]string{
			"message": msg,
			"type":    errType,
			"code":    code,
		},
	}
	out, _ := json.Marshal(payload)
	return out
}
