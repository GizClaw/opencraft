package applypatch

import (
	"context"
	"encoding/json"
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

func TestToolAppliesAddUpdateDelete(t *testing.T) {
	ws := memWorkspace(t)
	tool, err := New(ws)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	out, err := tool.Execute(ctx, `{"patch":"*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch\n"}`)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, `"action":"add"`) {
		t.Fatalf("add result = %s", out)
	}
	if data, err := ws.Read(ctx, "a.txt"); err != nil || string(data) != "hello\n" {
		t.Fatalf("a.txt = %q, %v", data, err)
	}

	if _, err := tool.Execute(ctx, `{"patch":"*** Begin Patch\n*** Update File: a.txt\n@@\n-hello\n+world\n*** End Patch\n"}`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if data, err := ws.Read(ctx, "a.txt"); err != nil || string(data) != "world\n" {
		t.Fatalf("a.txt after update = %q, %v", data, err)
	}

	out, err = tool.Execute(ctx, `{"patch":"*** Begin Patch\n*** Delete File: a.txt\n*** End Patch\n"}`)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := ws.Read(ctx, "a.txt"); err == nil {
		t.Fatal("a.txt still exists after delete")
	}
	var result struct {
		Files []struct {
			Path   string `json:"path"`
			Action string `json:"action"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil ||
		len(result.Files) != 1 || result.Files[0].Action != "delete" {
		t.Fatalf("delete result = %s, %v", out, err)
	}
}

func TestToolRejectsEscapingAndMalformedPatches(t *testing.T) {
	ws := memWorkspace(t)
	tool, err := New(ws)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, patch := range []string{
		"*** Begin Patch\n*** Add File: ../escape.txt\n+x\n*** End Patch\n",
		"*** Begin Patch\n*** Add File: /abs/path.txt\n+x\n*** End Patch\n",
		"no markers",
	} {
		if _, err := tool.Execute(ctx,
			`{"patch":`+jsonString(patch)+`}`); err == nil {
			t.Fatalf("Execute(%q) unexpectedly succeeded", patch)
		}
	}
	// An empty patch is valid and applies nothing.
	if _, err := tool.Execute(ctx,
		`{"patch":"*** Begin Patch\n*** End Patch\n"}`); err != nil {
		t.Fatalf("empty patch must succeed, got %v", err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
