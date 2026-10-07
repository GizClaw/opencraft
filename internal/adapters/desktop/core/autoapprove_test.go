package core

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/confirm"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// confirmOptions is the pair capabilities/tools/confirm raises; the
// predicate answers a spec only when it is that shape.
var confirmOptions = []interact.Option{
	{Label: "Yes", Value: confirm.OptionYes},
	{Label: "No", Value: confirm.OptionNo},
}

// TestAutoApproveConfirmFollowsThePersistedMode pins the decision the
// desktop prompt backend consults: the shared gate's yes/no card is
// answered only when the run's conversation is in YOLO mode, and every
// other shape — a different prompt source, another shape sharing the
// source, a card without the Yes option, a run this process did not
// mint, a workspace with no pooled Host, a missing conversation id —
// falls back to the interactive card.
func TestAutoApproveConfirmFollowsThePersistedMode(t *testing.T) {
	if profile.YoloOnly() {
		t.Skip("yoloonly builds pin every conversation to yolo")
	}
	c, workDir, _ := newInstallerTestCore(t)
	ctx := context.Background()
	h, err := c.Runtime.EnsureHost(ctx, workDir)
	if err != nil {
		t.Fatalf("ensure host: %v", err)
	}
	defer func() { _ = h.Close() }()

	conversationID := c.Conversation.New(workDir)
	c.Conversation.TrackRun(workDir, conversationID, "r-mode")
	spec := interact.Spec{
		ID:      "p-1",
		RunID:   "r-mode",
		Kind:    interact.KindConfirm,
		Source:  "opencraft.confirm",
		Title:   "Install skill?",
		Options: confirmOptions,
	}

	if _, ok := c.autoApproveConfirm(ctx, spec, conversationID); ok {
		t.Fatal("workspace mode auto-approved a confirmation")
	}
	if err := h.Sessions().SetMode(
		ctx, conversationID, sessions.ModeYOLO,
	); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	option, ok := c.autoApproveConfirm(ctx, spec, conversationID)
	if !ok || option != confirm.OptionYes {
		t.Fatalf("yolo mode replied %q/%v, want %q/true",
			option, ok, confirm.OptionYes)
	}
	if err := h.Sessions().SetMode(
		ctx, conversationID, sessions.ModeWorkspace,
	); err != nil {
		t.Fatalf("set mode back: %v", err)
	}
	if _, ok := c.autoApproveConfirm(ctx, spec, conversationID); ok {
		t.Fatal("mode change did not reach the decision")
	}
	if err := h.Sessions().SetMode(
		ctx, conversationID, sessions.ModeYOLO,
	); err != nil {
		t.Fatalf("restore yolo: %v", err)
	}

	// The identity is the fixed yes/no card on a run this process
	// minted, on a workspace whose Host is pooled: everything else
	// asks.
	otherWorkspace := t.TempDir()
	c.Conversation.TrackRun(otherWorkspace, conversationID, "r-nohost")
	for _, other := range []interact.Spec{
		{
			ID: "p-2", RunID: "r-mode", Kind: interact.KindConfirm,
			Source: "opencraft.request_permissions", Options: confirmOptions,
		},
		{
			ID: "p-3", RunID: "r-mode", Kind: interact.KindSelect,
			Source: "opencraft.confirm", Options: confirmOptions,
		},
		{
			ID: "p-4", RunID: "r-mode", Kind: interact.KindConfirm,
			Source: "opencraft.confirm",
			Options: []interact.Option{
				{Label: "Continue", Value: "continue"},
			},
		},
		{
			ID: "p-5", RunID: "r-unminted", Kind: interact.KindConfirm,
			Source: "opencraft.confirm", Options: confirmOptions,
		},
		{
			ID: "p-6", RunID: "r-nohost", Kind: interact.KindConfirm,
			Source: "opencraft.confirm", Options: confirmOptions,
		},
	} {
		if _, ok := c.autoApproveConfirm(ctx, other, conversationID); ok {
			t.Errorf("auto-approved %s (kind %q, source %q)",
				other.ID, other.Kind, other.Source)
		}
	}
	if _, ok := c.autoApproveConfirm(ctx, spec, ""); ok {
		t.Fatal("auto-approved a confirmation with no conversation id")
	}

	// The composition root installs the predicate: Ask itself answers
	// a yolo confirmation without registering a pending prompt.
	c.Prompt.SetRunConvResolver(c.Conversation.ConversationForRun)
	done := make(chan interact.Reply, 1)
	go func() {
		reply, err := c.Prompt.Ask(ctx, spec)
		if err != nil {
			t.Errorf("Ask: %v", err)
		}
		done <- reply
	}()
	select {
	case reply := <-done:
		if reply.Option == nil || *reply.Option != confirm.OptionYes {
			t.Fatalf("Ask reply = %+v, want %s", reply, confirm.OptionYes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ask did not auto-answer the yolo confirmation")
	}
}
