package fixtures

import (
	"encoding/json"
	"net/http"
)

// Bedrock (AWS JSON protocol) error bodies are {"message": "..."}; the
// exception type travels in the x-amzn-errortype response header, set by the
// catalog fixture, not here.
func bedrockErrorBody(status int) []byte {
	if b, ok := bedrockErrorBodies[status]; ok {
		return b
	}
	return marshalBedrockError(http.StatusText(status))
}

var bedrockErrorBodies = map[int][]byte{
	http.StatusTooManyRequests:    marshalBedrockError("Too many requests, please wait before trying again."),
	http.StatusServiceUnavailable: marshalBedrockError("The service is temporarily unable to process your request. Please try again later."),
	http.StatusRequestTimeout:     marshalBedrockError("The model did not respond within the allotted time."),
	http.StatusBadRequest:         marshalBedrockError("The provided input is invalid or exceeds the maximum allowed size."),
}

func marshalBedrockError(msg string) []byte {
	out, _ := json.Marshal(map[string]string{"message": msg})
	return out
}
