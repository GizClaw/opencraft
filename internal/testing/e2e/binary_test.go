package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestBinaryRunJSONL builds the real opencraft binary and verifies the
// `opencraft run --json` CLI contract: JSONL events on stdout and the
// exit code contract (0 completed / non-zero failed).
func TestBinaryRunJSONL(t *testing.T) {
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

	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hello from binary"})
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeInferenceConfig(t, configDir, provider.URL())

	cmd := exec.Command(bin, "run", "--json",
		"--workdir", workDir, "--config", configDir,
		"--prompt", "say hello")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opencraft run: %v\n%s", err, out)
	}
	var sawCompleted bool
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var ev rollout.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		if ev.Type == rollout.TypeTurnCompleted {
			sawCompleted = ev.Status == "completed"
		}
	}
	if !sawCompleted {
		t.Fatalf("missing turn.completed event:\n%s", out)
	}
}
