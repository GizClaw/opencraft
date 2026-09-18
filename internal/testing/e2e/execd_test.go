package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestBinaryRunExecCommandThroughExecd builds the real binary and drives
// one exec_command through the forked execd child (remote: true), so the
// path the in-process tests cannot reach - engine assembly, pool lease,
// self-fork, protobuf handshake, sandboxed command - runs end to end.
func TestBinaryRunExecCommandThroughExecd(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "opencraft")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "exec_command",
			Arguments: `{"command":"echo execd-e2e-ok"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL(), "remote: true")
	workDir := t.TempDir()

	// A fresh workspace has no dynamic approval and headless has no user
	// to ask, so the command has to be allowlisted up front.
	layout, err := config.ResolveWorkspace(home, workDir)
	if err != nil {
		t.Fatal(err)
	}
	approvals := []byte("version: v1\nallow:\n  - \"echo execd-e2e-ok\"\n")
	if err := os.WriteFile(layout.ApprovalsFile, approvals, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "run", "--json",
		"--workdir", workDir, "--config", configDir,
		"--prompt", "run the echo command")
	// The forked execd child resolves ~/.opencraft from HOME, so the run
	// stays isolated from the developer's real profile.
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencraft run: %v\n%s", err, out)
	}
	var sawResult bool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var ev rollout.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		if ev.Type == rollout.TypeItemToolResult && !ev.IsError &&
			strings.Contains(ev.Content, "execd-e2e-ok") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("no successful exec_command tool result in the rollout:\n%s", out)
	}
}
