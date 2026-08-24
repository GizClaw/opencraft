package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/config"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// TestDeleteSessionClosesLiveSession verifies the desktop deletion path
// at the runtime layer: deleting one conversation by key closes the
// session manager's live Session for that key (so a later Open starts
// fresh), and removing the conversation clears its session_settings row
// (think level / model hint). opencraft runs sessions ephemeral and the
// runtime has resume disabled, so the checkpoint store carries no
// per-session durable state for the manager to remove.
func TestDeleteSessionClosesLiveSession(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Providers: []config.KeyedProvider{{
			Provider:  config.Providers[0],
			KeySource: config.KeyEnv,
		}},
	}
	if err := config.WriteInference(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	projectDir := filepath.Join(work, ".opencraft", "config")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "opencraft.yaml"),
		[]byte("resources:\n  box:\n    settings:\n      remote: false\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	mgr, err := config.Open(config.Options{
		WorkDir: work,
		UserDir: userDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	value, ok := rt.Resource("sessions")
	if !ok {
		t.Fatal("sessions resource missing")
	}
	store, ok := value.(*ocsessions.Store)
	if !ok || store == nil {
		t.Fatal("sessions resource is not a *sessions.Store")
	}

	ctx := context.Background()
	key := coresession.Key{AgentID: "assistant", ContextID: "s-1"}
	if err := store.SetModel("s-1", "deepseek/deepseek-v4-flash"); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	lease, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := rt.Sessions().DeleteSession(ctx, key); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := lease.Session().Start(ctx,
		agent.Request{Message: message.NewTextMessage(message.RoleUser, "hi")},
	); !errors.Is(err, coresession.ErrSessionClosed) {
		t.Fatalf("Start on deleted session = %v, want ErrSessionClosed", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("lease.Close: %v", err)
	}
	// A new Open starts a fresh session (deletion is idempotent too).
	fresh, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		t.Fatalf("Open after delete: %v", err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatalf("fresh lease.Close: %v", err)
	}
	if err := rt.Sessions().DeleteSession(ctx, key); err != nil {
		t.Fatalf("second DeleteSession: %v", err)
	}

	// The store-level conversation removal also drops the settings row.
	if err := store.Remove("s-1"); err != nil {
		t.Fatalf("store.Remove: %v", err)
	}
	if model, err := store.Model("s-1"); err != nil || model != "" {
		t.Fatalf("Model after remove = %q, %v; want \"\", nil", model, err)
	}
}
