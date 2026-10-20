// Package faulttypes defines the data types describing a fault to inject.
// A Fault is either HTTP-level (proxy mode) or syscall-level (eBPF mode);
// mixing kinds is rejected at scenario load time. A delay is level-neutral:
// the experiment's match decides what it holds.
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

	// ResponseMutation rewrites part of the real upstream response instead
	// of replacing it. The only value is "mcp-schema-mismatch": in the
	// response to an MCP tools/call request, every top-level value of
	// result.structuredContent changes type. It is an HTTP-level field,
	// can't be combined with the other HTTP-level fields, and is absent from
	// the report JSON when unset.
	ResponseMutation string `yaml:"response_mutation,omitempty" json:",omitempty"`

	Errno string `yaml:"errno,omitempty"`

	// DelayMS holds the matched request for this many milliseconds before
	// the rest of the fault applies (or, if nothing else is set, before the
	// request is forwarded unchanged). DelayJitterMS adds a uniform random
	// [0, DelayJitterMS] on top. Both are level-neutral and absent from the
	// report JSON when unset.
	DelayMS       int `yaml:"delay_ms,omitempty" json:",omitempty"`
	DelayJitterMS int `yaml:"delay_jitter_ms,omitempty" json:",omitempty"`
}

// maxDelayMS caps DelayMS+DelayJitterMS at 5 minutes, the per-connection
// deadline of the forward proxy (martian), past which a held response can
// no longer be written.
const maxDelayMS = 300000

// mutationMCPSchemaMismatch is the only ResponseMutation value.
const mutationMCPSchemaMismatch = "mcp-schema-mismatch"

// IsHTTP reports whether f sets an HTTP-level field. The level-neutral
// delay fields don't count.
func (f Fault) IsHTTP() bool {
	return f.HTTPStatus != 0 ||
		len(f.ResponseHeaders) > 0 ||
		f.ResponseBody != "" ||
		f.StreamCutoffTokens > 0 ||
		f.ResponseMutation != ""
}

// IsSyscall reports whether f sets a syscall-level field. The
// level-neutral delay fields don't count.
func (f Fault) IsSyscall() bool {
	return f.Errno != ""
}

var (
	ErrFaultMixed = errors.New("fault mixes HTTP-level and syscall-level fields")
	ErrFaultEmpty = errors.New("fault is empty: set http_status, response_headers, response_body, errno, or delay_ms")
)

func (f Fault) Validate() error {
	if f.IsHTTP() && f.IsSyscall() {
		return ErrFaultMixed
	}
	if err := f.validateDelay(); err != nil {
		return err
	}
	if !f.IsHTTP() && !f.IsSyscall() && f.DelayMS == 0 {
		return ErrFaultEmpty
	}
	if f.HTTPStatus != 0 && (f.HTTPStatus < 100 || f.HTTPStatus > 599) {
		return fmt.Errorf("http_status %d is outside [100,599]", f.HTTPStatus)
	}
	if f.StreamCutoffTokens < 0 {
		return fmt.Errorf("stream_cutoff_tokens %d must be non-negative", f.StreamCutoffTokens)
	}
	return f.validateMutation()
}

// validateMutation accepts only the known ResponseMutation value, and only
// on its own: the other HTTP-level fields replace or cut the upstream
// response that a mutation edits.
func (f Fault) validateMutation() error {
	if f.ResponseMutation == "" {
		return nil
	}
	if f.ResponseMutation != mutationMCPSchemaMismatch {
		return fmt.Errorf("response_mutation %q is not one of: %s", f.ResponseMutation, mutationMCPSchemaMismatch)
	}
	if f.HTTPStatus != 0 || len(f.ResponseHeaders) > 0 || f.ResponseBody != "" || f.StreamCutoffTokens != 0 {
		return errors.New("response_mutation can't be combined with http_status, response_headers, response_body or stream_cutoff_tokens")
	}
	return nil
}

func (f Fault) validateDelay() error {
	if f.DelayMS < 0 {
		return fmt.Errorf("delay_ms %d must be non-negative", f.DelayMS)
	}
	if f.DelayJitterMS < 0 {
		return fmt.Errorf("delay_jitter_ms %d must be non-negative", f.DelayJitterMS)
	}
	if f.DelayJitterMS > 0 && f.DelayMS == 0 {
		return errors.New("delay_jitter_ms requires delay_ms")
	}
	// Compare without adding, so huge values can't overflow past the cap.
	if f.DelayMS > maxDelayMS || f.DelayJitterMS > maxDelayMS-f.DelayMS {
		return fmt.Errorf("delay_ms + delay_jitter_ms must not exceed %d (5 minutes)", maxDelayMS)
	}
	return nil
}
