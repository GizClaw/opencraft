package host

import (
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// TestHostReportsTheStoresCheckpointCapabilities pins the wiring that turns the
// checkpoint journal on: the runtime asks the *host*, and a host that does not
// answer falls back to "persists, no increments" — one full board record per
// wave, which is the cost the journal exists to remove. The answer must be the
// store's (the session store journals), not a constant.
func TestHostReportsTheStoresCheckpointCapabilities(t *testing.T) {
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.CloseDB() }()

	h := &Host{store: store}
	caps := h.CheckpointCapabilities()
	if !caps.Enabled {
		t.Fatal("host reports no checkpoint store, but the session store persists")
	}
	if caps.Journal == nil {
		t.Fatal("host reports no journal, but the session store implements one")
	}

	// A host without a store persists nothing, and must say so rather than
	// hand the engine a nil store's capabilities.
	if caps := (&Host{}).CheckpointCapabilities(); caps.Enabled {
		t.Fatal("a host with no store reports an enabled checkpoint target")
	}
	if caps := (*Host)(nil).CheckpointCapabilities(); caps.Enabled {
		t.Fatal("a nil host reports an enabled checkpoint target")
	}
}
