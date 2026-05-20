// Package provider defines the LLM provider interface plus shared types used
// across the OpenAI and Anthropic implementations.
package provider

import (
	"context"
	"errors"

	"github.com/salience-cli/salience/internal/detect"
)

// Response is the normalized output of one LLM call.
type Response struct {
	Text         string
	Sources      []detect.Source
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
