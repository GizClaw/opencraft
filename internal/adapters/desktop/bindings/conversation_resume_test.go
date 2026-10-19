package bindings

import (
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/inferencetest"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
)

// TestResumeSessionRefusesAnotherWorkspacesConversation covers the
// banner-click contract: a click that names its workspace reopens a
// conversation there, and a click that lost its workspace must not
// adopt a conversation living in some other workspace's store.
// Focusing such an id leaves the window on an empty chat that no event
// can fill, and - because the next start trusts the workspace's current
// conversation - makes that turn run under an id this store does not
// own.
func TestResumeSessionRefusesAnotherWorkspacesConversation(t *testing.T) {
	provider := inferencetest.NewScriptedOpenAI(t,
		inferencetest.ScriptedReply{Text: "done"})
	configDir := t.TempDir()
	writeConversationRetryConfig(t, configDir, provider.URL())
	home := t.TempDir()
	other := t.TempDir()

	c := core.NewCore(configDir, t.TempDir(), home)
	ctx := c.Shell.Context()
	c.SetWorkDir(home)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime: %v", err)
	}
	b := NewConversationBinding(c)

	homeStore, releaseHome := openWorkspaceSessions(t, c, home)
	defer releaseHome()
	homeID, err := homeStore.Create()
	if err != nil {
		t.Fatalf("create home conversation: %v", err)
	}

	// The window moves on, and the click names only the conversation: a
	// banner from a producer that predates the workspace field.
	c.SetWorkDir(other)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime for the second workspace: %v", err)
	}
	err = b.ResumeSession(homeID)
	if err == nil {
		t.Fatal("resume adopted a conversation of another workspace")
	}
	if !strings.Contains(err.Error(), "is not in workspace") {
		t.Fatalf("resume error = %v, want the ownership refusal", err)
	}
	if got := c.Conversation.Current(other); got != "" {
		t.Fatalf("current conversation = %q, want the workspace untouched", got)
	}

	// A conversation this workspace does own still opens.
	otherStore, releaseOther := openWorkspaceSessions(t, c, other)
	defer releaseOther()
	otherID, err := otherStore.Create()
	if err != nil {
		t.Fatalf("create other conversation: %v", err)
	}
	if err := b.ResumeSession(otherID); err != nil {
		t.Fatalf("resume of the workspace's own conversation: %v", err)
	}
	if got := c.Conversation.Current(other); got != otherID {
		t.Fatalf("current conversation = %q, want %q", got, otherID)
	}
}
