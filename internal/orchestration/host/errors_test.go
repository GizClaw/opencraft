package host

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
)

func TestIsRetryableStartError(t *testing.T) {
	retryable := []error{
		ErrRuntimeNotReady,
		ErrRuntimeClosing,
		ErrSessionStoreNotReady,
		fmt.Errorf("wrapped: %w", ErrRuntimeClosing),
	}
	for _, err := range retryable {
		if !IsRetryableStartError(err) {
			t.Errorf("IsRetryableStartError(%v) = false, want true", err)
		}
	}

	notRetryable := []error{
		nil,
		context.Canceled,
		errors.New("host: session \"s-1\" is deleted or being deleted"),
		errors.New("host: deletion already in progress for session \"s-1\""),
		errors.New("host: invalid session id \"nope\""),
		errors.New("host: message role is required"),
		errors.New("host: start turn: boom"),
	}
	for _, err := range notRetryable {
		if IsRetryableStartError(err) {
			t.Errorf("IsRetryableStartError(%v) = true, want false", err)
		}
	}
}

func TestSentinelsMatchPublicMessages(t *testing.T) {
	for _, tt := range []struct {
		err error
		msg string
	}{
		{ErrRuntimeNotReady, "host: runtime is not ready"},
		{ErrRuntimeClosing, "host: runtime is closing"},
		{ErrSessionStoreNotReady, "host: session store is not ready"},
	} {
		if got := tt.err.Error(); got != tt.msg {
			t.Errorf("sentinel message = %q, want %q", got, tt.msg)
		}
	}
}

// TestFailureRequestID extracts the provider request identifier from
// wrapped engine failures, mirroring the chain a truncated stream
// produces, and falls back to the inference error field for errors
// that carry the id without an errdefs marker.
func TestFailureRequestID(t *testing.T) {
	truncated := fmt.Errorf(
		"graph %q node %q: %w",
		"opencraft-assistant", "llm",
		inference.NewError(
			inference.ProviderTruncated, inference.OperationGenerate, "",
			errdefs.WithRequestID(
				errdefs.NotAvailable(errors.New("stream ended early")),
				"req-stream-1",
			),
		),
	)
	if got := failureRequestID(truncated); got != "req-stream-1" {
		t.Fatalf("failureRequestID(truncated) = %q, want req-stream-1", got)
	}

	field := inference.NewError(
		inference.ProviderFailure, inference.OperationGenerate, "",
		errors.New("provider boom"),
	)
	field.RequestID = "req-field-1"
	if got := failureRequestID(field); got != "req-field-1" {
		t.Fatalf("failureRequestID(field) = %q, want req-field-1", got)
	}

	if got := failureRequestID(nil); got != "" {
		t.Fatalf("failureRequestID(nil) = %q, want empty", got)
	}
	if got := failureRequestID(errors.New("plain")); got != "" {
		t.Fatalf("failureRequestID(plain) = %q, want empty", got)
	}
}
