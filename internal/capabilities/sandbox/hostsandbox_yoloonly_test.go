//go:build yoloonly

package sandbox

import (
	"path/filepath"
	"testing"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// TestYoloOnlyBuildRoutesEverySessionUnconfined verifies the profile
// guarantee at the sandbox boundary: no session in a yoloonly binary
// may reach the confined runner.
func TestYoloOnlyBuildRoutesEverySessionUnconfined(t *testing.T) {
	confined := &recordRunner{}
	unconfined := &recordRunner{}
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
	}

	if _, err := hs.Start(
		sessionCtx(id), coresandbox.SessionSpec{ID: "yolo-only"},
	); err != nil {
		t.Fatal(err)
	}
	if confined.count() != 0 || unconfined.count() != 1 {
		t.Fatalf(
			"yoloonly session routing: %d confined / %d unconfined starts",
			confined.count(), unconfined.count())
	}
}

// TestYoloOnlyBuildHostWorkspaceEscapesConfines verifies host file
// access in a yoloonly binary: reads and writes may reach any path.
func TestYoloOnlyBuildHostWorkspaceEscapesConfines(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	confinedWS, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	hw := &HostWorkspace{
		sessions: newTestStore(t),
		confined: confinedWS,
		host:     &hostWorkspace{root: root},
	}
	ctx := sessionCtx("s1")

	if err := hw.Write(ctx, outside, []byte("s3cret")); err != nil {
		t.Fatalf("yolo write outside root: %v", err)
	}
	data, err := hw.Read(ctx, outside)
	if err != nil || string(data) != "s3cret" {
		t.Fatalf("yolo read = %q, %v", data, err)
	}
}
