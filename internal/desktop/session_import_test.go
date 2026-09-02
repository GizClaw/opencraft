package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func TestExportSessionBundleRoundTrips(t *testing.T) {
	a, store := newSessionOpsApp(t)
	a.workDir = t.TempDir()

	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "bundle me"),
		message.NewTextMessage(message.RoleAssistant, "done"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsage(context.Background(), id, ocsessions.Usage{TotalTokens: 7}); err != nil {
		t.Fatal(err)
	}

	path, err := a.ExportSessionBundle(id)
	if err != nil {
		t.Fatalf("ExportSessionBundle: %v", err)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("bundle path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var req ocsessions.ImportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if req.Source != "opencraft:"+id {
		t.Fatalf("source = %q", req.Source)
	}
	if len(req.Turns) != 1 || len(req.Turns[0].Messages) != 2 {
		t.Fatalf("bundle turns = %+v", req.Turns)
	}
}
