package execd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	opencraftBinOnce sync.Once
	opencraftBin     string
	opencraftBinErr  error
)

// buildOpencraft compiles the main binary once per test run so the
// self-fork path (socketpair + Hello + Bind) is exercised end to end.
func buildOpencraft(t *testing.T) string {
	t.Helper()
	opencraftBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "opencraft-fork-test")
		if err != nil {
			opencraftBinErr = err
			return
		}
		// `go build -o opencraft` appends .exe on Windows, so the path
		// the tests exec has to carry the same suffix.
		name := "opencraft"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		opencraftBin = filepath.Join(dir, name)
		root, err := filepath.Abs(filepath.Join("..", "..", ".."))
		if err != nil {
			opencraftBinErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", opencraftBin, ".")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			opencraftBinErr = fmt.Errorf("build opencraft: %v\n%s", err, out)
		}
	})
	if opencraftBinErr != nil {
		t.Fatal(opencraftBinErr)
	}
	return opencraftBin
}

func TestLaunchBindExecAndStop(t *testing.T) {
	requirePOSIXChild(t)
	bin := buildOpencraft(t)
	ctx := context.Background()
	client, stop, err := LaunchExe(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(ctx, &Start{
		ProcessId: "fork",
		Argv:      []string{"/bin/sh", "-c", "echo forked-ok"},
	}); err != nil {
		t.Fatal(err)
	}
	var output []byte
	var after int64
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", output)
		}
		read, err := client.Read(ctx, &Read{
			ProcessId: "fork", AfterSeq: after, MaxBytes: 4096,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range read.GetChunks() {
			output = append(output, chunk.GetData()...)
		}
		after = read.GetNextSeq()
		if read.GetEof() {
			break
		}
	}
	if !strings.Contains(string(output), "forked-ok") {
		t.Fatalf("output = %q", output)
	}
}

func TestStopKillsChildProcessGroups(t *testing.T) {
	requirePOSIXChild(t)
	bin := buildOpencraft(t)
	ctx := context.Background()
	client, stop, err := LaunchExe(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Bind(ctx, t.TempDir(), &SandboxPolicy{}); err != nil {
		t.Fatal(err)
	}
	marker := "sleep 47.5"
	if _, err := client.Start(ctx, &Start{
		ProcessId: "sleep",
		Argv:      []string{"/bin/sh", "-c", marker},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if pgrepMarker(marker) == "" {
		t.Fatal("marker process is not running")
	}
	stop()
	deadline := time.Now().Add(5 * time.Second)
	for pgrepMarker(marker) != "" {
		if time.Now().After(deadline) {
			t.Fatalf("process survived stop: %s", pgrepMarker(marker))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLaunchReportsChildStderr(t *testing.T) {
	requirePOSIXChild(t)
	// /bin/sh cannot execute the child's argv ("execd"), so it dies
	// immediately with a diagnostic on stderr. Launch must surface it
	// instead of only reporting a closed connection.
	_, _, err := LaunchExe(context.Background(), "/bin/sh")
	if err == nil {
		t.Fatal("expected the launch to fail")
	}
	if !strings.Contains(err.Error(), "execd") {
		t.Fatalf("error = %v, want the child's stderr", err)
	}
}

// TestLaunchFailsFastWhenTheContextIsCancelled pins that the handshake
// honours the caller's context: this is what lets Pool.Close abort an
// in-flight pre-warm instead of waiting out the handshake timeout.
func TestLaunchFailsFastWhenTheContextIsCancelled(t *testing.T) {
	bin := buildOpencraft(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _, err := LaunchExe(ctx, bin)
	if err == nil {
		t.Fatal("launch with a cancelled context succeeded")
	}
	elapsed := time.Since(start)
	t.Logf("cancelled launch returned after %v", elapsed)
	if elapsed > 8*time.Second {
		t.Fatalf("cancelled launch took %v, want a fast failure", elapsed)
	}
}
