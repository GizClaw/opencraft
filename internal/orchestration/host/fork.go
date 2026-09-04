package host

import (
	"context"
	"errors"
	"fmt"

	fcmemory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// ForkConversation creates a new session whose transcript contains the
// source conversation through sourceRunID, copies attachment files and
// settings, and seeds the fork's memory with exactly that prefix so the
// next turn can continue with the same context.
func (h *Host) ForkConversation(
	ctx context.Context, sourceID, sourceRunID string,
) (string, error) {
	if h == nil || h.store == nil {
		return "", errors.New("host: session store is not ready")
	}
	if h.ctrl == nil || h.ctrl.Runtime() == nil {
		return "", errors.New("host: runtime is not ready")
	}
	forked, err := h.store.Fork(ctx, sourceID, sourceRunID)
	if err != nil {
		return "", err
	}
	if err := h.seedForkMemory(ctx, forked); err != nil {
		if removeErr := h.store.Remove(ctx, forked.ID); removeErr != nil {
			telemetry.WarnErr(ctx,
				"host: remove forked session after memory seed failure",
				removeErr, otellog.String("conversation.id", forked.ID))
		}
		return "", fmt.Errorf("host: fork memory seed %s: %w", forked.ID, err)
	}
	return forked.ID, nil
}

func (h *Host) seedForkMemory(
	ctx context.Context,
	forked ocsessions.ForkResult,
) error {
	value, ok := h.ctrl.Runtime().Resource("mem")
	if !ok {
		return errors.New("host: memory resource is missing")
	}
	sink, ok := value.(fcmemory.TurnSink)
	if !ok {
		return errors.New("host: memory resource is not a turn sink")
	}
	var msgs []message.Message
	for _, turn := range forked.Turns {
		msgs = append(msgs, turn.Messages...)
	}
	if len(msgs) == 0 {
		return errors.New("host: forked conversation has no messages to seed")
	}
	return opmemory.SeedConversation(
		ctx, sink, forked.ID, "fork:"+forked.ID, msgs,
	)
}
