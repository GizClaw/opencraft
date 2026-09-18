//go:build windows

package execd

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// pipeSecurityDescriptor grants full access to the pipe's owner and
// SYSTEM only. The pipe name is random but not secret; the ACL is the
// boundary.
const pipeSecurityDescriptor = "D:P(A;;GA;;;OW)(A;;GA;;;SY)"

// prepareChannel creates a named pipe listener. Windows has no
// socketpair, so the child dials the pipe by name after it starts.
func prepareChannel() (*channelPlan, error) {
	name := `\\.\pipe\opencraft-execd-` + randomChannelSuffix()
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor,
	})
	if err != nil {
		return nil, fmt.Errorf("execd: listen pipe: %w", err)
	}
	plan := &channelPlan{args: []string{"-execd-pipe", name}}
	plan.attach = func(ctx context.Context) (net.Conn, error) {
		type result struct {
			conn net.Conn
			err  error
		}
		accepted := make(chan result, 1)
		go func() {
			conn, err := listener.Accept()
			accepted <- result{conn: conn, err: err}
		}()
		select {
		case r := <-accepted:
			return r.conn, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	plan.close = func() { _ = listener.Close() }
	return plan, nil
}

// childChannel dials the parent's named pipe. It only runs in the
// execd child process.
func ChildChannel(_ int, pipe string) (net.Conn, error) {
	if pipe == "" {
		return nil, errors.New("execd: -execd-pipe is required")
	}
	// A parent that dies before accepting must not leave this process
	// blocking forever; the journal sweep only runs on the next host
	// start.
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, pipe)
	if err != nil {
		return nil, fmt.Errorf("execd: dial pipe: %w", err)
	}
	return conn, nil
}
