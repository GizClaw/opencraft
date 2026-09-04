package host

import (
	"context"
	"errors"
	"fmt"
	"strings"

	fcmemory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// ImportSession writes one neutral session bundle into the Host's
// store and seeds memory so a later turn can continue with the same
// context. Store.Import dedupes by Source; when the conversation was
// already imported and completed, the call returns the existing id
// without touching memory.
func (h *Host) ImportSession(
	ctx context.Context, req ocsessions.ImportRequest,
) (string, error) {
	if h == nil || h.store == nil {
		return "", errors.New("host: session store is not ready")
	}
	if h.ctrl == nil || h.ctrl.Runtime() == nil {
		return "", errors.New("host: runtime is not ready")
	}

	h.importMu.Lock()
	defer h.importMu.Unlock()

	id, err := h.store.Import(ctx, req)
	if err != nil {
		return "", err
	}
	ready, err := h.store.ImportReady(ctx, id)
	if err != nil {
		h.abortImport(ctx, id)
		return "", fmt.Errorf("host: import readiness %s: %w", id, err)
	}
	if ready {
		return id, nil
	}
	if err := h.seedImportMemory(ctx, id, req); err != nil {
		h.abortImport(ctx, id)
		return "", fmt.Errorf("host: import memory seed %s: %w", id, err)
	}
	if err := h.store.CompleteImport(ctx, id); err != nil {
		h.abortImport(ctx, id)
		return "", fmt.Errorf("host: import complete %s: %w", id, err)
	}
	return id, nil
}

func (h *Host) abortImport(ctx context.Context, id string) {
	if err := h.store.AbortImport(ctx, id); err != nil {
		telemetry.WarnErr(ctx, "host: abort failed import failed", err,
			otellog.String("conversation.id", id))
	}
}

func (h *Host) seedImportMemory(
	ctx context.Context, id string, req ocsessions.ImportRequest,
) error {
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return errors.New("host: import source is required for memory seed")
	}
	value, ok := h.ctrl.Runtime().Resource("mem")
	if !ok {
		return errors.New("host: memory resource is missing")
	}
	sink, ok := value.(fcmemory.TurnSink)
	if !ok {
		return errors.New("host: memory resource is not a turn sink")
	}
	var msgs []message.Message
	for _, turn := range req.Turns {
		msgs = append(msgs, turn.Messages...)
	}
	if len(msgs) == 0 {
		return errors.New("host: imported session has no messages to seed")
	}
	return opmemory.SeedConversation(ctx, sink, id, source, msgs)
}
