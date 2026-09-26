package procmem

import (
	"errors"
	"os"
	"testing"
)

// TestSnapshotReadsThisProcess is the live check: whatever the platform
// probe can see, the family it returns must contain the process that asked
// for it, with a footprint of its own.
func TestSnapshotReadsThisProcess(t *testing.T) {
	family, err := Snapshot()
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no process probe on this platform")
	}
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	self, ok := family.Member(os.Getpid())
	if !ok {
		t.Fatalf("family of %d does not contain the process itself: %+v",
			os.Getpid(), family.Members)
	}
	if self.Role != RoleSelf {
		t.Errorf("self role = %q, want %q", self.Role, RoleSelf)
	}
	if self.Footprint == 0 {
		t.Errorf("self footprint = 0, want the process's own memory")
	}
	if family.Root != os.Getpid() {
		t.Errorf("root = %d, want %d", family.Root, os.Getpid())
	}
	if family.Total() < self.Footprint {
		t.Errorf("total = %d, want at least the self footprint %d",
			family.Total(), self.Footprint)
	}
}
