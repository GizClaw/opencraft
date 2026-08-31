package sandbox

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

// recordingRunner records calls so the noTTYRunner decorator can be
// verified as a pure pass-through (minus the TTY surface).
type recordingRunner struct {
	caps       coresandbox.Capabilities
	started    []coresandbox.SessionSpec
	listed     bool
	terminated string
	closed     bool
}

func (r *recordingRunner) Close() error {
	r.closed = true
	return nil
}

func (r *recordingRunner) Capabilities() coresandbox.Capabilities {
	return r.caps
}

func (r *recordingRunner) Start(_ context.Context, spec coresandbox.SessionSpec) (coresandbox.Session, error) {
	r.started = append(r.started, spec)
	return nil, nil
}

func (r *recordingRunner) List(context.Context) ([]coresandbox.SessionInfo, error) {
	r.listed = true
	return nil, nil
}

func (r *recordingRunner) Terminate(_ context.Context, id string) error {
	r.terminated = id
	return nil
}

func TestNoTTYRunnerClearsTTYCapability(t *testing.T) {
	inner := &recordingRunner{caps: coresandbox.Capabilities{
		Policy: coresandbox.Enforcement{EnvAllowList: true, MemoryCap: true},
		Features: coresandbox.SessionFeatures{
			TTY:    true,
			Signal: true,
		},
	}}
	caps := noTTYRunner{inner}.Capabilities()
	if caps.Features.TTY {
		t.Error("noTTYRunner must clear the TTY feature")
	}
	if !caps.Features.Signal {
		t.Error("noTTYRunner must keep the other session features")
	}
	if !caps.Policy.EnvAllowList || !caps.Policy.MemoryCap {
		t.Error("noTTYRunner must keep the policy surface")
	}
}

func TestNoTTYRunnerRejectsTTYStart(t *testing.T) {
	inner := &recordingRunner{}
	_, err := noTTYRunner{inner}.Start(context.Background(), coresandbox.SessionSpec{
		ID:  "s1",
		TTY: true,
	})
	if !errdefs.IsNotAvailable(err) {
		t.Fatalf("Start(TTY=true) error = %v, want NotAvailable", err)
	}
	if len(inner.started) != 0 {
		t.Fatalf("inner runner must not be reached; started = %v", inner.started)
	}
}

func TestNoTTYRunnerForwardsNonTTYStart(t *testing.T) {
	inner := &recordingRunner{}
	spec := coresandbox.SessionSpec{ID: "s1", Argv: []string{"echo", "hi"}}
	r := noTTYRunner{inner}
	if _, err := r.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start(TTY=false) error = %v", err)
	}
	if len(inner.started) != 1 || inner.started[0].ID != spec.ID {
		t.Fatalf("inner started = %v, want %+v", inner.started, spec)
	}
}

func TestNoTTYRunnerForwardsLifecycle(t *testing.T) {
	inner := &recordingRunner{}
	r := noTTYRunner{inner}

	ctx := context.Background()
	if _, err := r.List(ctx); err != nil {
		t.Fatalf("List error = %v", err)
	}
	if !inner.listed {
		t.Error("List must be forwarded to the inner runner")
	}
	if err := r.Terminate(ctx, "s9"); err != nil {
		t.Fatalf("Terminate error = %v", err)
	}
	if inner.terminated != "s9" {
		t.Errorf("Terminate forwarded id = %q, want s9", inner.terminated)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !inner.closed {
		t.Error("Close must be forwarded to the inner runner")
	}
}
