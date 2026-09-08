package bindings

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestStartTurnRetriesAfterHostRetirement covers the binding contract:
// when the current Host retired between the frontend send and
// StartRun, StartTurn waits for the replacement Host inside the same
// RPC and succeeds without duplicating the user message's session.
func TestStartTurnRetriesAfterHostRetirement(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeConversationRetryConfig(t, configDir, provider.URL())

	c := core.NewCore(configDir, dataDir, "")
	ctx := c.Shell.Context()
	c.SetWorkDir(workDir)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime: %v", err)
	}
	old := c.Runtime.Current()
	if old == nil {
		t.Fatal("no current host after rebuild")
	}

	// Retire the idle Host. Runtime.current still points at the closed
	// Host, which is exactly the state StartTurn must recover from.
	if err := old.Close(); err != nil {
		t.Fatalf("close host: %v", err)
	}
	if !old.IsClosing() {
		t.Fatal("closed host must report IsClosing")
	}
	if got := c.Runtime.Current(); got != old {
		t.Fatalf("current after Close = %p, want the retired host %p",
			got, old)
	}

	b := NewConversationBinding(c)
	start, err := b.StartTurn(StartTurnRequest{
		Message: message.NewTextMessage(
			message.RoleUser, "hello after retire"),
	})
	if err != nil {
		t.Fatalf("StartTurn across host retirement: %v", err)
	}
	if start.RunID == "" || start.ConversationID == "" {
		t.Fatalf("start result = %+v, want run and conversation ids", start)
	}

	replacement := c.Runtime.Current()
	if replacement == nil || replacement == old {
		t.Fatalf("current after retry = %p, want a replacement host", replacement)
	}
	if replacement.IsClosing() || replacement.IsStale() {
		t.Fatalf("replacement host closing=%v stale=%v, want usable host",
			replacement.IsClosing(), replacement.IsStale())
	}

	// The retried start must have minted exactly one conversation:
	// the optimistic user message is owned by the frontend, and the
	// failed first attempt never wrote any session rows.
	metas, err := replacement.Sessions().List()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("sessions after retried start = %d, want exactly 1", len(metas))
	}
	if metas[0].ID != start.ConversationID {
		t.Fatalf("session id = %q, want %q", metas[0].ID, start.ConversationID)
	}

	deadline := time.Now().Add(15 * time.Second)
	for provider.Calls() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("provider never received the retried turn")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writeConversationRetryConfig(t *testing.T, configDir, baseURL string) {
	t.Helper()
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), seed, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      "openai",
			Name:      "fake",
			API:       "chat",
			Endpoint:  baseURL,
			Enabled:   true,
			KeySource: config.KeyLiteral,
			KeyValue:  "test-key",
			Models:    []config.Model{{Name: "fake-model"}},
		}},
	}
	if err := config.WriteInference(configDir, cfg); err != nil {
		t.Fatal(err)
	}
}
