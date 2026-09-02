package memory

import (
	"context"
	"errors"
	"strings"

	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/memory/summary"
)

// SeedConversation pushes an imported conversation into the summary
// memory assembly so a later OpenCraft turn can continue with the same
// context. Messages are rendered the same way the commit hook renders
// live turns (tool calls/results become text), and the assembly folds a
// summary for histories longer than the raw window.
//
// The caller is responsible for keeping the sourceID unique for a
// conversation: Store.Import dedupes by its own Source key, and the
// idempotency key here mirrors that source so a successful import is
// never seeded twice.
func SeedConversation(
	ctx context.Context,
	assembly *summary.Assembly,
	conversationID, sourceID string,
	msgs []message.Message,
) error {
	if assembly == nil {
		return errors.New("memory: seed assembly is nil")
	}
	if strings.TrimSpace(conversationID) == "" {
		return errors.New("memory: seed conversation id is required")
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return errors.New("memory: seed source id is required")
	}

	rendered := renderConversation(msgs)
	if len(rendered) == 0 {
		return errors.New("memory: seed conversation has no text-bearing messages")
	}

	// Imported system prompts describe the *source* application's own
	// environment and conflict with OpenCraft's world-state sections.
	// Keep them in the session archive but out of the memory raw
	// window; user/assistant text and tool activity are the context a
	// continuation actually needs.
	seed := rendered[:0]
	for _, m := range rendered {
		if m.Role == message.RoleSystem {
			continue
		}
		seed = append(seed, m)
	}
	if len(seed) == 0 {
		return errors.New("memory: seed conversation has no user/assistant/tool messages")
	}

	return assembly.CommitTurn(ctx, corememory.Turn{
		Scope: corememory.Scope{
			// Matches the scope the world-state context provider reads
			// with. The local SQLite adapter does not partition by
			// scope today, but keeping them aligned avoids a future
			// split silently losing seeded imports.
			RuntimeID: "opencraft",
		},
		ConversationID: conversationID,
		IdempotencyKey: sourceID,
		Messages:       seed,
		Metadata: corememory.Metadata{
			"origin":    "session.import",
			"source_id": sourceID,
		},
	})
}
