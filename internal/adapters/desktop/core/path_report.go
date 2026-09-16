package core

import (
	"sync"

	"github.com/GizClaw/opencraft/internal/foundation/utils/envpath"
)

// pathReport holds the last PATH resolution this process performed. The
// diagnostics view reads it to explain where every PATH entry came from;
// re-deriving that from the environment afterwards cannot recover the
// provenance, because a directory the resolver appended looks like an
// inherited entry on the next pass.
type pathReport struct {
	mu     sync.Mutex
	result envpath.Result
	set    bool
}

// SetPathReport records the outcome of one PATH resolution.
func (c *Core) SetPathReport(result envpath.Result) {
	c.path.mu.Lock()
	defer c.path.mu.Unlock()
	c.path.result = result
	c.path.set = true
}

// PathReport returns the last PATH resolution and whether one ran in this
// process.
func (c *Core) PathReport() (envpath.Result, bool) {
	c.path.mu.Lock()
	defer c.path.mu.Unlock()
	return c.path.result, c.path.set
}
