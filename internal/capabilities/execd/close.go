package execd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// closeLog closes c, logging only unexpected failures: an already
// closed handle and a peer that hung up are ordinary teardown.
func closeLog(ctx context.Context, what string, closer io.Closer) {
	if closer == nil {
		return
	}
	err := closer.Close()
	if err == nil ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, net.ErrClosed) {
		return
	}
	telemetry.WarnErr(ctx, what, err)
}
