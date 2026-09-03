package sandbox

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

// noTTYRunner decorates a sandbox.Runner and reports no interactive
// session capability. flowcraft's Windows backend advertises TTY even
// when constructed with write confinement, but then rejects TTY starts
// at runtime; opencraft disables interactive sessions on Windows
// (issue #38), so the wrapper keeps the advertised surface honest and
// rejects TTY requests before they reach the backend.
type noTTYRunner struct{ inner coresandbox.Runner }

func (r noTTYRunner) Start(
	ctx context.Context,
	spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	if spec.TTY {
		return nil, errdefs.NotAvailablef(
			"opencraft sandbox: interactive sessions are disabled on Windows")
	}
	return r.inner.Start(ctx, spec)
}

func (r noTTYRunner) List(ctx context.Context) ([]coresandbox.SessionInfo, error) {
	return r.inner.List(ctx)
}

func (r noTTYRunner) Terminate(ctx context.Context, id string) error {
	return r.inner.Terminate(ctx, id)
}

func (r noTTYRunner) Capabilities() coresandbox.Capabilities {
	caps := r.inner.Capabilities()
	caps.Features.TTY = false
	return caps
}

func (r noTTYRunner) Close() error {
	return r.inner.Close()
}
