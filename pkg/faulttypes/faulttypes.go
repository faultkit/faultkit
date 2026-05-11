// Package faulttypes defines the data types describing a fault to inject.
// A Fault is either HTTP-level (proxy mode) or syscall-level (eBPF mode);
// mixing kinds is rejected at scenario load time.
package faulttypes

import (
	"errors"
	"fmt"
)

// Fault describes a single fault to inject.
type Fault struct {
	HTTPStatus         int               `yaml:"http_status,omitempty"`
	ResponseHeaders    map[string]string `yaml:"response_headers,omitempty"`
	ResponseBody       string            `yaml:"response_body,omitempty"`
	StreamCutoffTokens int               `yaml:"stream_cutoff_tokens,omitempty"`

	Errno string `yaml:"errno,omitempty"`
}

func (f Fault) IsHTTP() bool {
	return f.HTTPStatus != 0 ||
		len(f.ResponseHeaders) > 0 ||
		f.ResponseBody != "" ||
		f.StreamCutoffTokens > 0
}

func (f Fault) IsSyscall() bool {
	return f.Errno != ""
}

var (
	ErrFaultMixed = errors.New("fault mixes HTTP-level and syscall-level fields")
	ErrFaultEmpty = errors.New("fault is empty: set http_status, response_headers, response_body, or errno")
)

func (f Fault) Validate() error {
	if f.IsHTTP() && f.IsSyscall() {
		return ErrFaultMixed
	}
	if !f.IsHTTP() && !f.IsSyscall() {
		return ErrFaultEmpty
	}
	if f.HTTPStatus != 0 && (f.HTTPStatus < 100 || f.HTTPStatus > 599) {
		return fmt.Errorf("http_status %d is outside [100,599]", f.HTTPStatus)
	}
	if f.StreamCutoffTokens < 0 {
		return fmt.Errorf("stream_cutoff_tokens %d must be non-negative", f.StreamCutoffTokens)
	}
	return nil
}
