package bindings

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestStartTurnRoutesToOwningWorkspace covers the queued-draft
// contract: a turn for a conversation that lives in one workspace
// keeps running in that workspace's runtime and session store even
// when the window has already moved to another workspace, and a start
// for an id no store owns is refused instead of being attached as a
// brand-new session in whichever workspace happens to be active.
func TestStartTurnRoutesToOwningWorkspace(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	configDir := t.TempDir()
	writeConversationRetryConfig(t, configDir, provider.URL())
	home := t.TempDir()
	other := t.TempDir()

	c := core.NewCore(configDir, t.TempDir(), "")
	ctx := c.Shell.Context()
	c.SetWorkDir(home)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime: %v", err)
	}
	b := NewConversationBinding(c)

	// The conversation starts in its own workspace, which is also the
	// one on screen.
	convID := c.Conversation.New(home)
	if _, err := b.StartTurn(StartTurnRequest{
		ContextID: convID,
		Workspace: home,
		Message:   message.NewTextMessage(message.RoleUser, "home turn"),
	}); err != nil {
		t.Fatalf("start in the owning workspace: %v", err)
	}
	homeStore, releaseHome := openWorkspaceSessions(t, c, home)
	defer releaseHome()
	waitForTurns(t, homeStore, convID, 1)

	// Opening another chat in that workspace moves its current
	// conversation away from convID, so the drain has to resolve
	// ownership from the store instead of the minted-id shortcut.
	otherHomeID := c.Conversation.New(home)
	if otherHomeID == convID {
		t.Fatal("second mint reused the first conversation id")
	}

	// The window switches to a second workspace while the draft stays
	// staged behind the still-running turn of the first one.
	c.SetWorkDir(other)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime for the second workspace: %v", err)
	}
	active := c.Runtime.Current()
	if active == nil || !core.SameWorkspace(active.WorkDir(), other) {
		t.Fatalf("current host = %v, want the second workspace", active)
	}

	// The staged draft fires now. It must run where its conversation
	// lives, not in the workspace on screen.
	drained, err := b.StartTurn(StartTurnRequest{
		ContextID: convID,
		Workspace: home,
		Message: message.NewTextMessage(
			message.RoleUser, "staged draft",
		),
	})
	if err != nil {
		t.Fatalf("drain into the owning workspace: %v", err)
	}
	if drained.ConversationID != convID || drained.RunID == "" {
		t.Fatalf("drained start = %+v, want the original conversation", drained)
	}
	// The drain is background work: it must not take over the Host the
	// UI talks to.
	if got := c.Runtime.Current(); got != active {
		t.Fatalf("background drain replaced the current host: %v", got)
	}
	waitForTurns(t, homeStore, convID, 2)
	otherStore, releaseOther := openWorkspaceSessions(t, c, other)
	defer releaseOther()
	if metas := listSessions(t, otherStore); len(metas) != 0 {
		t.Fatalf(
			"on-screen workspace gained %d sessions from the drain: %+v",
			len(metas), metas)
	}

	// A start for a conversation no workspace owns is refused: it must
	// not mint a session in the workspace on screen.
	if _, err := b.StartTurn(StartTurnRequest{
		ContextID: sessions.NewID(),
		Workspace: other,
		Message:   message.NewTextMessage(message.RoleUser, "stray"),
	}); err == nil {
		t.Fatal("start for an unowned conversation id was accepted")
	}
	if metas := listSessions(t, otherStore); len(metas) != 0 {
		t.Fatalf("refused start created %d sessions: %+v", len(metas), metas)
	}

	// A stale owner hint (the registry label of a conversation actor
	// can lag a workspace switch) is corrected from the stores instead
	// of being believed: the turn still lands in the owning workspace
	// and nothing appears in the workspace the hint named.
	c.SetWorkDir(home)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild runtime for the owning workspace: %v", err)
	}
	if _, err := b.StartTurn(StartTurnRequest{
		ContextID: convID,
		Workspace: other,
		Message:   message.NewTextMessage(message.RoleUser, "stale hint"),
	}); err != nil {
		t.Fatalf("start with a stale owner hint: %v", err)
	}
	waitForTurns(t, homeStore, convID, 3)
	if metas := listSessions(t, otherStore); len(metas) != 0 {
		t.Fatalf("stale hint created %d sessions: %+v", len(metas), metas)
	}
}

// openWorkspaceSessions returns a shared handle on one workspace's
// session store without going through the active Host.
func openWorkspaceSessions(
	t *testing.T, c *core.Core, workDir string,
) (*sessions.Store, func()) {
	t.Helper()
	layout, err := c.ResolveLayout(workDir)
	if err != nil {
		t.Fatalf("resolve layout for %s: %v", workDir, err)
	}
	mgr := c.Runtime.Manager()
	store, err := mgr.OpenSessions(
		context.Background(), workDir, layout, 40,
	)
	if err != nil {
		t.Fatalf("open sessions for %s: %v", workDir, err)
	}
	return store, func() { mgr.ReleaseSessions(store) }
}

func listSessions(t *testing.T, store *sessions.Store) []sessions.Meta {
	t.Helper()
	metas, err := store.List()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return metas
}

// waitForTurns waits until one conversation's settled turns reach want:
// the archive write happens on the run's settle path, so this also
// proves the turn really persisted in the store it was routed to.
func waitForTurns(
	t *testing.T, store *sessions.Store, convID string, want int,
) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last []sessions.Meta
	for {
		last = listSessions(t, store)
		for _, meta := range last {
			if meta.ID == convID && meta.Turns >= want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"conversation %s never reached %d turns: %+v",
				convID, want, last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
