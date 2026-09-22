//go:build windows

package guilock

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// pipeSecurityDescriptor grants full access to the pipe's owner and
// SYSTEM only. The pipe name is derived from the state root, not secret,
// so the ACL is the boundary (the same stance as the execd channel).
const pipeSecurityDescriptor = "D:P(A;;GA;;;OW)(A;;GA;;;SY)"

// endpointName is the holder's raise endpoint. Windows named pipes have
// no directory structure, so the name comes from the launch's identity
// and nothing else.
func endpointName(_, identity string) string {
	return `\\.\pipe\opencraft-gui-` + endpointToken(identity)
}

// listenEndpoint binds the raise endpoint. The caller holds the lock, so
// a pipe name that is somehow still in use belongs to a dead holder's
// handle - one that the kernel has already released with the process.
func listenEndpoint(endpoint string) (net.Listener, error) {
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor,
	})
	if err != nil {
		return nil, fmt.Errorf("guilock: listen %s: %w", endpoint, err)
	}
	return listener, nil
}

// dialEndpoint reaches the holder's endpoint. go-winio maps the absence
// of a listener to a fast failure; the caller reports a raise that did
// not happen instead of retrying.
func dialEndpoint(ctx context.Context, endpoint string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("guilock: dial %s: %w", endpoint, err)
	}
	return conn, nil
}

// removeEndpoint is a no-op on Windows: the pipe disappears with the
// listener handle.
func removeEndpoint(context.Context, string) {}
