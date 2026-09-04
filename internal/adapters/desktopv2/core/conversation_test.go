package core

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestConversationNewAndSettings(t *testing.T) {
	c := NewConversation()
	id := c.New()
	if len(id) < 3 || id[:2] != "s-" {
		t.Fatalf("conversation id = %q", id)
	}
	if c.Mode() != sessions.ModeWorkspace {
		t.Fatalf("mode = %q", c.Mode())
	}
	c.SetMode(sessions.ModeYOLO)
	c.SetThink("high")
	c.SetModel("deepseek/deepseek-v4")
	if c.Mode() != sessions.ModeYOLO ||
		c.Think() != "high" ||
		c.Model() != "deepseek/deepseek-v4" {
		t.Fatalf("settings not applied: %+v", c)
	}
}
