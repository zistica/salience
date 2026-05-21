// Package provider defines the LLM provider interface plus shared types used
// across the OpenAI and Anthropic implementations.
package provider

import (
	"context"
	"errors"

	"github.com/salience-cli/salience/internal/detect"
)

// ToolCall is one provider-side tool invocation the LLM made while
// answering — typically a web_search query. We normalise across vendors
// (OpenAI Responses, Anthropic server_tool_use, Perplexity citations)
// to make the dashboard's "Anatomy" view vendor-agnostic.
//
// Kind is "web_search" for the most common case. Query is the actual
// search string the LLM emitted. ResultCount is how many results that
// tool call returned (0 if the API doesn't expose it).
type ToolCall struct {
	Kind        string `json:"kind"`
	Query       string `json:"query,omitempty"`
	ResultCount int    `json:"result_count,omitempty"`
}

// Response is the normalized output of one LLM call.
type Response struct {
	Text         string
	Sources      []detect.Source
	ToolCalls    []ToolCall
	InputTokens  int
	OutputTokens int
	RawJSON      string
}

// Provider is implemented by both OpenAI and Anthropic backends.
type Provider interface {
	Name() string
	Kind() string
	Model() string
	Call(ctx context.Context, prompt string, maxTokens int, temperature *float64) (*Response, error)
}

// ErrAuth is returned for 401/403 responses and signals the runner to abort.
var ErrAuth = errors.New("authentication failed")

// ErrTransient wraps any error that should trigger a retry with backoff.
type ErrTransient struct{ Err error }

func (e *ErrTransient) Error() string { return "transient: " + e.Err.Error() }
func (e *ErrTransient) Unwrap() error { return e.Err }

// Transient wraps err as a retryable error.
func Transient(err error) error { return &ErrTransient{Err: err} }

// IsTransient reports whether err is (or wraps) an ErrTransient.
func IsTransient(err error) bool {
	var t *ErrTransient
	return errors.As(err, &t)
}
