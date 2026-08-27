package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeHooks(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !m.Empty() {
		t.Fatal("missing file must yield an empty manager")
	}
}

func TestLoadRejectsBadMatcher(t *testing.T) {
	path := writeHooks(t, `{
		"hooks": {
			"PreToolUse": [{"matcher": "(", "hooks": [{"command": "true"}]}]
		}
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("bad matcher must be rejected")
	}
}

func TestFireRunsMatchingHookWithStdinPayload(t *testing.T) {
	out := filepath.Join(t.TempDir(), "hook.out")
	hooksJSON := `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "^exec_command$", "hooks": [{"command": "cat > ` + out + `", "timeout": 5}]}
			]
		}
	}`
	m, err := Load(writeHooks(t, hooksJSON))
	if err != nil {
		t.Fatal(err)
	}
	m.Fire(context.Background(), EventPreToolUse, map[string]any{
		"event": "PreToolUse",
		"tool":  "exec_command",
	})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("hook stdin is not JSON: %v", err)
	}
	if payload["tool"] != "exec_command" {
		t.Fatalf("payload tool = %v", payload["tool"])
	}

	// Non-matching tool and other events do not fire.
	out2 := filepath.Join(t.TempDir(), "hook2.out")
	m.Fire(context.Background(), EventPreToolUse, map[string]any{"tool": "read_file"})
	if _, err := os.Stat(out2); !os.IsNotExist(err) {
		t.Fatal("hook fired for non-matching tool")
	}
	m.Fire(context.Background(), EventTurnEnd, map[string]any{"tool": "exec_command"})
	if _, err := os.Stat(out2); !os.IsNotExist(err) {
		t.Fatal("hook fired for wrong event")
	}
}

func TestFireSkipsMissingHookFileAndEmptyManager(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Must not panic.
	m.Fire(context.Background(), EventPreToolUse, map[string]any{"tool": "x"})
}

func TestFireMatcherPrecedence(t *testing.T) {
	out := filepath.Join(t.TempDir(), "hook.out")
	cfg := `{
		"hooks": {
			"SessionStart": [{"matcher": "^startup$", "hooks": [{"command": "cat > ` + out + `"}]}]
		}
	}`
	m, err := Load(writeHooks(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	// source=startup matches; tool-based values do not.
	m.Fire(context.Background(), EventSessionStart,
		map[string]any{"event": EventSessionStart, "source": "startup"})
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("session hook with source=startup did not fire: %v", err)
	}
	out2 := filepath.Join(t.TempDir(), "hook2.out")
	cfg2 := `{
		"hooks": {
			"SessionStart": [{"matcher": "^resume$", "hooks": [{"command": "cat > ` + out2 + `"}]}]
		}
	}`
	m2, err := Load(writeHooks(t, cfg2))
	if err != nil {
		t.Fatal(err)
	}
	m2.Fire(context.Background(), EventSessionStart,
		map[string]any{"event": EventSessionStart, "source": "new"})
	if _, err := os.Stat(out2); !os.IsNotExist(err) {
		t.Fatal("matcher must filter by source")
	}
}

func TestLoadSkipsNonCommandHookTypes(t *testing.T) {
	hooksJSON := `{
		"hooks": {
			"PreToolUse": [
				{"matcher": "*", "hooks": [
					{"type": "mcp_tool", "server": "x", "tool": "y"},
					{"type": "command", "command": "true"}
				]}
			]
		}
	}`
	m, err := Load(writeHooks(t, hooksJSON))
	if err != nil {
		t.Fatal(err)
	}
	if m.Empty() {
		t.Fatal("command hook should survive the non-command filter")
	}
	if len(m.groups[EventPreToolUse][0].hooks) != 1 {
		t.Fatalf("hooks = %+v, want only the command hook", m.groups[EventPreToolUse])
	}
}
