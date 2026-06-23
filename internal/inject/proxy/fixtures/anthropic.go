package fixtures

import (
	"encoding/json"
	"net/http"
)

// Anthropic uses HTTP 529 for "overloaded" — not in net/http constants.
const statusOverloaded = 529

func anthropicErrorBody(status int) []byte {
	if b, ok := anthropicErrorBodies[status]; ok {
		return b
	}
	return marshalAnthropicError(http.StatusText(status), "api_error")
}

var anthropicErrorBodies = map[int][]byte{
	http.StatusTooManyRequests:       marshalAnthropicError("Number of requests has exceeded your rate limit. Please try again later.", "rate_limit_error"),
	http.StatusServiceUnavailable:    marshalAnthropicError("The server is temporarily unable to handle requests. Please try again later.", "overloaded_error"),
	statusOverloaded:                 marshalAnthropicError("Overloaded", "overloaded_error"),
	http.StatusInternalServerError:   marshalAnthropicError("Internal server error", "api_error"),
	http.StatusGatewayTimeout:        marshalAnthropicError("Request timed out", "api_error"),
	http.StatusRequestEntityTooLarge: marshalAnthropicError("The request exceeds the maximum allowed size.", "request_too_large"),
}

func marshalAnthropicError(msg, errType string) []byte {
	payload := map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errType,
			"message": msg,
		},
	}
	out, _ := json.Marshal(payload)
	return out
}
