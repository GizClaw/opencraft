package applypatch

import (
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/workspace"
)

func memWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestToolDefinition(t *testing.T) {
	tool, err := New(memWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if def.Name != Name || !strings.Contains(def.Description, "Begin Patch") {
		t.Fatalf("definition = %+v", def)
	}
	if !tool.Metadata().MutatesState {
		t.Fatal("apply_patch must be mutating")
	}
}
