package worldstate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// The rendered replay cache: a turn that appended nothing reuses the previous
// render (checked against one transcript cursor, not a decode), and a turn that
// appended rows renders again. These cases drive the real service over a real
// session store, because the hit is defined by the store's cursor.

func replayService(t *testing.T, store *sessions.Store) *Service {
	t.Helper()
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)
	svc.memory = replayMemory{stubMemory{items: []corememory.ContextItem{}}}
	return svc
}

func TestReplayHistoryReusesTheRenderUntilTheTranscriptMoves(t *testing.T) {
	ctx := context.Background()
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.CloseDB() }()

	conv, err := store.Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	svc := replayService(t, store)

	// Nothing appended yet: the first call renders (and caches) an empty
	// replay, and asking again before the transcript moves must not read
	// memory at all.
	first := svc.replayHistory(ctx, conv)
	svc.memory = replayMemory{stubMemory{items: []corememory.ContextItem{{
		Kind:        corememory.ContextRawMessage,
		MessageRole: message.RoleUser,
		Content:     message.NewTextContent("late row"),
	}}}}
	if again := svc.replayHistory(ctx, conv); len(again) != len(first) {
		t.Fatalf("replay changed without the transcript moving: %d vs %d rows",
			len(again), len(first))
	}

	// Append one archived turn: the cursor moves and the render must follow.
	if err := store.AppendTurnWithRunID(ctx, conv, "run-1", []message.Message{
		message.NewTextMessage(message.RoleUser, "fresh row"),
	}); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	moved := svc.replayHistory(ctx, conv)
	if len(moved) == 0 {
		t.Fatal("the replay after the transcript moved is empty")
	}
}

func TestReplayCacheKeepsTheNewestEntryWithinItsBudget(t *testing.T) {
	cache := newReplayCache()
	big := []message.Message{
		message.NewTextMessage(message.RoleUser, strings.Repeat("x", replayChars)),
	}
	cache.put("s-1", 10, big)
	cache.put("s-2", 20, []message.Message{
		message.NewTextMessage(message.RoleUser, "small"),
	})
	// 8 Mi characters spent on s-1 leaves no room for s-2's entry unless the
	// oldest goes; the newest always stays.
	if _, ok := cache.get("s-1", 10); ok {
		t.Fatal("the oversized entry survived the budget once another conversation arrived")
	}
	if _, ok := cache.get("s-2", 20); !ok {
		t.Fatal("the newest entry was dropped")
	}
}

func TestReplayCacheMissesOnAStaleCursor(t *testing.T) {
	cache := newReplayCache()
	cache.put("s-1", 10, []message.Message{
		message.NewTextMessage(message.RoleUser, "row"),
	})
	if _, ok := cache.get("s-1", 11); ok {
		t.Fatal("a render for cursor 10 was served for cursor 11")
	}
	if _, ok := cache.get("s-1", 10); !ok {
		t.Fatal("the render for its own cursor missed")
	}
}
