package execd

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

// TestRemoteRunnerCloseThroughApproval verifies the #318 regression
// end to end: a remote runner wrapped by sandbox.WithApproval must be
// an io.Closer, and Close must terminate the forked execd child and
// remove its socket.
func TestRemoteRunnerCloseThroughApproval(t *testing.T) {
	bin := buildOpencraft(t)
	ctx := context.Background()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	client, sock, stop, err := LaunchExe(ctx, root, bin, "")
	if err != nil {
		t.Fatalf("LaunchExe: %v", err)
	}
	t.Cleanup(stop)
	runner, err := NewRemoteRunner(ctx, client, stop)
	if err != nil {
		t.Fatalf("NewRemoteRunner: %v", err)
	}
	wrapped := sandbox.WithApproval(runner, nil, nil)

	closer, ok := wrapped.(io.Closer)
	if !ok {
		t.Fatal("decorated remote runner must implement io.Closer")
	}

	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket %s should exist while the child is alive: %v", sock, err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket %s should be removed after Close, stat err = %v", sock, err)
	}
	out, err := exec.Command("pgrep", "-f",
		sock).CombinedOutput()
	if err == nil {
		t.Fatalf("execd child still running after Close: %s", out)
	}
}
