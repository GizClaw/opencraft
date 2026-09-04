package host

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"os"
	"strings"
	"text/template"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

//go:embed assets/title.gotmpl
var titleTemplateRaw string

var titleTemplate = template.Must(template.New("title").Parse(titleTemplateRaw))

type titlePromptData struct {
	MaxWords int
}

// AutoTitle generates a short conversation title once after a turn
// finishes. A manual title (conversation_state "title") always wins.
func (h *Host) AutoTitle(ctx context.Context, contextID string) {
	id := ConversationID(contextID)
	h.mu.Lock()
	store := h.store
	ctrl := h.ctrl
	if h.titling[id] {
		h.mu.Unlock()
		return
	}
	h.titling[id] = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.titling, id)
		h.mu.Unlock()
	}()

	if store == nil || ctrl == nil || ctrl.Runtime() == nil {
		return
	}
	var custom string
	if err := store.ReadState(contextID, "title", &custom); err == nil {
		if strings.TrimSpace(custom) != "" {
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		telemetry.WarnErr(ctx, "host: read custom title failed", err,
			otellog.String("session", contextID))
	}
	first, err := store.FirstUserMessage(contextID)
	if err != nil {
		telemetry.Warn(ctx, "host: auto title history load failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
		return
	}
	first = strings.TrimSpace(first)
	if first == "" {
		return
	}
	value, ok := ctrl.Runtime().Resource("router")
	if !ok {
		return
	}
	router, ok := value.(*route.Router)
	if !ok || router == nil {
		return
	}
	maxTokens := 64
	textIntent := &inference.TextIntent{MaxOutputTokens: &maxTokens}
	if cfg, err := config.LoadInference(h.userDir); err == nil &&
		cfg.ModelReasoning("") {
		reasoning := false
		textIntent.ReasoningEnabled = &reasoning
	}
	response, _, err := router.Generate(ctx, inference.GenerateRequest{
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
				Intent: inference.Intent{Text: textIntent},
			},
		},
	})
	if err != nil {
		telemetry.WarnErr(ctx, "host: auto title generation failed", err,
			otellog.String("session", contextID))
		return
	}
	if h.usage != nil {
		h.usage(ctx, response.Usage)
	}
	// Title generation is a real model call against this session: feed
	// its usage into the session total and the user-level model_usage
	// tables instead of only showing it as a transient UI event.
	h.persistTurnUsage(ctx, contextID, usageFromReport(response.Usage))
	title := strings.TrimSpace(response.Message.Content.Text())
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return
	}
	const maxTitle = 70
	runes := []rune(title)
	if len(runes) > maxTitle {
		title = string(runes[:maxTitle]) + "…"
	}
	if err := store.WriteState(contextID, "title", title); err != nil {
		telemetry.WarnErr(ctx, "host: persist auto title failed", err,
			otellog.String("session", contextID))
		return
	}
	h.mu.Lock()
	fn := h.sessionUpd
	h.mu.Unlock()
	if fn != nil {
		fn(ctx, contextID)
	}
	telemetry.Info(ctx, "host: auto title generated",
		otellog.String("session", contextID),
		otellog.Int("title_chars", len(title)))
}

func titleSystemContent() message.Content {
	var buf bytes.Buffer
	if err := titleTemplate.Execute(&buf, titlePromptData{MaxWords: 8}); err != nil {
		panic(err)
	}
	return message.Content{Parts: []message.Part{
		message.TextPart{Text: buf.String()},
	}}
}
