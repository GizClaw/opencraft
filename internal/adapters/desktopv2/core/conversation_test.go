package core

import (
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestConversationNewAndSettings(t *testing.T) {
	c := NewConversation()
	id := c.New("/tmp/w")
	if len(id) < 3 || id[:2] != "s-" {
		t.Fatalf("conversation id = %q", id)
	}
	if c.Mode("/tmp/w") != sessions.ModeWorkspace {
		t.Fatalf("mode = %q", c.Mode("/tmp/w"))
	}
	c.SetMode("/tmp/w", sessions.ModeYOLO)
	c.SetThink("/tmp/w", "high")
	c.SetModel("/tmp/w", "deepseek/deepseek-v4")
	if c.Mode("/tmp/w") != sessions.ModeYOLO ||
		c.Think("/tmp/w") != "high" ||
		c.Model("/tmp/w") != "deepseek/deepseek-v4" {
		t.Fatalf("settings not applied: %+v", c)
	}
}

func TestConversationStateIsWorkspaceScoped(t *testing.T) {
	c := NewConversation()
	workA := filepath.Join(t.TempDir(), "a")
	workB := filepath.Join(t.TempDir(), "b")
	idA := c.New(workA)
	idB := c.New(workB)
	if idA == idB {
		t.Fatalf("workspaces minted the same conversation id %q", idA)
	}
	if got := c.Current(workA); got != idA {
		t.Fatalf("current in %s = %q, want %q", workA, got, idA)
	}
	if got := c.Current(workB); got != idB {
		t.Fatalf("current in %s = %q, want %q", workB, got, idB)
	}
	if c.Current(filepath.Join(t.TempDir(), "c")) != "" {
		t.Fatal("unknown workspace must not have a current session")
	}

	c.SetMode(workA, sessions.ModeYOLO)
	c.SetThink(workA, "high")
	c.SetModel(workA, "deepseek/deepseek-v4")
	if c.Mode(workB) != sessions.ModeWorkspace ||
		c.Think(workB) != string(sessions.ThinkMedium) ||
		c.Model(workB) != "" {
		t.Fatalf("workspace b picked up workspace a settings: %+v", c)
	}

	c.TrackRun(idA, "r-a")
	c.TrackRun(idB, "r-b")
	if got := c.ConversationForRun("r-a"); got != idA {
		t.Fatalf("run r-a owned by %q, want %q", got, idA)
	}
	if got := c.ConversationForRun("r-b"); got != idB {
		t.Fatalf("run r-b owned by %q, want %q", got, idB)
	}
}

func TestConversationWorkspaceKeyUsesCanonicalPath(t *testing.T) {
	c := NewConversation()
	workDir := filepath.Join(t.TempDir(), "repo")
	spelled := filepath.Join(workDir, "sub", "..")

	id := c.New(spelled)
	if got := c.Current(workDir); got != id {
		t.Fatalf("canonical path current = %q, want %q", got, id)
	}
	c.SetMode(filepath.Join(workDir, "."), sessions.ModeYOLO)
	if got := c.Mode(spelled); got != sessions.ModeYOLO {
		t.Fatalf("alternate path spelling mode = %q, want yolo", got)
	}
}

func TestConversationNewUsesConfiguredDefaults(t *testing.T) {
	c := NewConversation()
	c.SetDefaults(sessions.ModeReadOnly, "high")
	c.New("/tmp/w")
	if c.Mode("/tmp/w") != sessions.ModeReadOnly ||
		c.Think("/tmp/w") != "high" ||
		c.Model("/tmp/w") != "" {
		t.Fatalf(
			"minted settings = (%q, %q, %q)",
			c.Mode("/tmp/w"), c.Think("/tmp/w"), c.Model("/tmp/w"),
		)
	}
}

func TestConversationGettersFallBackToConfiguredDefaults(t *testing.T) {
	c := NewConversation()
	// A workspace with no minted/resumed conversation yet must report
	// the configured defaults, not the hardcoded first-run values.
	c.SetDefaults(sessions.ModeYOLO, "minimal")
	if got := c.Mode("/tmp/untouched"); got != sessions.ModeYOLO {
		t.Fatalf("mode = %q, want yolo", got)
	}
	if got := c.Think("/tmp/untouched"); got != "minimal" {
		t.Fatalf("think = %q, want minimal", got)
	}
}
