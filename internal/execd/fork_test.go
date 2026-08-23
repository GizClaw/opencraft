package execd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// self-fork path can be exercised end to end.
func buildOpencraft(t *testing.T) string {
	t.Helper()
	opencraftBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "opencraft-fork-test")
		if err != nil {
			opencraftBinErr = err
			return
		}
		opencraftBin = filepath.Join(dir, "opencraft")
		root, err := filepath.Abs(filepath.Join("..", ".."))
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

func TestLaunchForksExecServer(t *testing.T) {
	bin := buildOpencraft(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	client, _, stop, err := LaunchExe(ctx, root, bin, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "fork",
		Argv:      []string{"/bin/sh", "-c", "echo forked-ok"},
	}); err != nil {
		t.Fatal(err)
	}
	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", all)
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "fork", AfterSeq: &after, MaxBytes: intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range read.Chunks {
			all = append(all, ch.Data...)
		}
		after = read.NextSeq
		if read.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "forked-ok") {
		t.Errorf("output = %q", all)
	}
}

// TestLaunchAppliesSandboxPolicy verifies the parent's serialized
// sandbox policy reaches the execd child and becomes the default
// environment for spawned processes: allow replaces the forwarded host
// variables and inject adds values.
func TestLaunchAppliesSandboxPolicy(t *testing.T) {
	bin := buildOpencraft(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	client, _, stop, err := LaunchExe(ctx, root, bin,
		`{"env_policy":{"allow":["PATH"],"inject":{"OPENCRAFT_TEST_MARKER":"policy-ok"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "policy",
		Argv:      []string{"/bin/sh", "-c", "echo $OPENCRAFT_TEST_MARKER"},
	}); err != nil {
		t.Fatal(err)
	}
	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", all)
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "policy", AfterSeq: &after, MaxBytes: intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range read.Chunks {
			all = append(all, ch.Data...)
		}
		after = read.NextSeq
		if read.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "policy-ok") {
		t.Errorf("output = %q, want injected marker", all)
	}
}
