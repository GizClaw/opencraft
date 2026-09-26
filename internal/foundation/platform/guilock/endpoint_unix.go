//go:build !windows

package guilock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// socketName is the raise endpoint inside the state root. A unix socket
// is per-user through the directory it lives in: the state root is 0700
// and belongs to the account that runs the GUI.
const socketName = "gui.sock"

// socketPathLimit is how long a socket path may get. sockaddr_un's
// sun_path is 104 bytes on macOS and 108 on Linux, terminator included;
// the margin keeps room for a trailing suffix. A state root that deep is
// rare (a --data-dir under a scratch tree) and only loses the raise, not
// the lock.
const socketPathLimit = 100

// endpointName is where the holder listens for raise requests. The state
// root is the natural place - the lock already lives there, and the
// directory is the access boundary - with the temporary directory as the
// fallback for roots too deep for a socket address. Both sides derive
// the name the same way, so the fallback never splits them.
func endpointName(stateRoot, identity string) string {
	path := filepath.Join(stateRoot, socketName)
	if len(path) <= socketPathLimit {
		return path
	}
	return filepath.Join(os.TempDir(), "opencraft-gui-"+endpointToken(identity)+".sock")
}

// listenEndpoint binds the raise endpoint. The caller holds the lock, so
// anything still answering on this path belongs to a holder that died;
// clearing it is safe and the only chance to clean up after a crash.
func listenEndpoint(endpoint string) (net.Listener, error) {
	if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("guilock: clear stale endpoint: %w", err)
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("guilock: listen %s: %w", endpoint, err)
	}
	return listener, nil
}

// dialEndpoint reaches the holder's endpoint. A missing path or a closed
// listener fails fast; the caller reports a raise that did not happen
// instead of retrying.
func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("guilock: dial %s: %w", endpoint, err)
	}
	return conn, nil
}

// removeEndpoint drops the socket file on the way out; a holder that dies
// without doing so leaves a stale path the next holder clears.
func removeEndpoint(ctx context.Context, endpoint string) {
	err := os.Remove(endpoint)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return
	}
	telemetry.WarnErr(ctx, "guilock: raise endpoint cleanup failed", err,
		otellog.String("endpoint", endpoint))
}
