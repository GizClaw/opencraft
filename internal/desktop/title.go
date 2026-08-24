package desktop

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// autoTitle generates a short conversation title from the first user
// message once, after a turn finishes, and persists it as the session's
// title (a manual rename always wins: it is written to the same slot
// and never overwritten here). Generation is best-effort — a missing
// router, an unconfigured install, or a provider failure keeps the
// first-message fallback the sessions list already uses.
func (a *App) autoTitle(ctx context.Context, contextID string) {
	a.mu.Lock()
	store := a.sessions
	ctrl := a.ctrl
	if a.titling == nil {
		a.titling = make(map[string]bool)
	}
	if a.titling[contextID] {
		a.mu.Unlock()
		return
	}
	a.titling[contextID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.titling, contextID)
		a.mu.Unlock()
	}()

	if store == nil || ctrl == nil || ctrl.Runtime() == nil {
		return
	}
	var custom string
	if store.ReadState(contextID, "title", &custom) == nil &&
		strings.TrimSpace(custom) != "" {
		return
	}
	msgs, err := store.History(ctx, contextID, 0)
	if err != nil {
		telemetry.Warn(ctx, "desktop: auto title history load failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
		return
	}
	var first string
	for _, m := range msgs {
		if m.Role == message.RoleUser {
			first = strings.TrimSpace(m.Content.Text())
			break
		}
	}
	if first == "" {
		telemetry.Warn(ctx, "desktop: auto title skipped, no user message",
			otellog.String("session", contextID),
			otellog.Int("messages", len(msgs)))
		return
	}
	value, ok := ctrl.Runtime().Resource("router")
	if !ok {
		telemetry.Warn(ctx, "desktop: auto title skipped, router resource missing",
			otellog.String("session", contextID))
		return
	}
	router, ok := value.(*route.Router)
	if !ok || router == nil {
		telemetry.Warn(ctx, "desktop: auto title skipped, router resource is not a router",
			otellog.String("session", contextID))
		return
	}
	maxTokens := 16
	response, _, err := router.Generate(ctx, inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: first},
				}},
				Intent: inference.Intent{Text: &inference.TextIntent{
					MaxOutputTokens: &maxTokens,
				}},
			},
		},
	})
	if err != nil {
		telemetry.Warn(ctx, "desktop: auto title generation failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
		return
	}
	title := strings.TrimSpace(response.Message.Content.Text())
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		telemetry.Warn(ctx, "desktop: auto title generation returned empty",
			otellog.String("session", contextID))
		return
	}
	const maxTitle = 70
	runes := []rune(title)
	if len(runes) > maxTitle {
		title = string(runes[:maxTitle]) + "…"
	}
	if err := store.WriteState(contextID, "title", title); err == nil {
		a.bridge.Emit("session_updated", map[string]string{"id": contextID})
	} else {
		telemetry.Warn(ctx, "desktop: auto title write failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
	}
}
