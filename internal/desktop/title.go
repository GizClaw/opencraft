package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"strings"
	"text/template"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

//go:embed assets/title.gotmpl
var titleTemplateRaw string

var titleTemplate = template.Must(template.New("title").Parse(titleTemplateRaw))

// titlePromptData feeds the title prompt template.
type titlePromptData struct {
	MaxWords int
}

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
	msgs, err := store.History(ctx, contextID, -1)
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
	maxTokens := 64
	reasoning := false
	response, _, err := router.Generate(ctx, inference.GenerateRequest{
		// Instruct the model to act as a title generator: without this
		// it answers the user's first message instead of naming the
		// conversation.
		Context: []message.Message{{
			Role:    message.RoleSystem,
			Content: titleSystemContent(),
		}},
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: first},
				}},
				Intent: inference.Intent{Text: &inference.TextIntent{
					MaxOutputTokens: &maxTokens,
					// Title generation must not spend its budget on
					// reasoning: a thinking model would burn all tokens
					// on the trace and return empty text.
					ReasoningEnabled: &reasoning,
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
			otellog.String("session", contextID),
			otellog.Int("response_parts", len(response.Message.Content.Parts)),
			otellog.String("finish_reason", string(response.FinishReason)),
			otellog.Int64("output_tokens", response.Usage.OutputTokens))
		return
	}
	const maxTitle = 70
	runes := []rune(title)
	if len(runes) > maxTitle {
		title = string(runes[:maxTitle]) + "…"
	}
	if err := store.WriteState(contextID, "title", title); err == nil {
		a.bridge.Emit("session_updated", map[string]string{"id": contextID})
		telemetry.Info(ctx, "desktop: auto title generated",
			otellog.String("session", contextID),
			otellog.Int("title_chars", len(title)))
	} else {
		telemetry.Warn(ctx, "desktop: auto title write failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
	}
}

// titleSystemContent renders the embedded gotmpl title prompt into a
// system message. Rendering cannot fail for the fixed embedded
// template; a panic would only surface a programming error, so the
// result is trusted.
func titleSystemContent() message.Content {
	var buf bytes.Buffer
	if err := titleTemplate.Execute(&buf, titlePromptData{MaxWords: 8}); err != nil {
		panic(err)
	}
	return message.Content{Parts: []message.Part{
		message.TextPart{Text: buf.String()},
	}}
}
