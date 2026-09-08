package host

import (
	"context"
	"errors"
	"fmt"
	"testing"
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
