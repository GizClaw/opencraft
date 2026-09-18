package execd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
)

// channelPlan describes how the parent hands one exec child its private
// IPC channel. The per-OS files provide the implementation: Unix uses a
// socketpair passed as fd 3, Windows uses a named pipe.
type channelPlan struct {
	// args are appended to the child's argv.
	args []string
	// extraFiles are inherited by the child (Unix socketpair end).
	extraFiles []*os.File
	// attach returns the parent's end after the child has started.
	attach func(ctx context.Context) (net.Conn, error)
	// close releases channel resources the parent still owns.
	close func()
}

// randomChannelSuffix keeps two concurrent children (and their journal
// entries) from colliding.
func randomChannelSuffix() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(raw[:])
}
