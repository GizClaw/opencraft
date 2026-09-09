package desktop

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	otellog "go.opentelemetry.io/otel/log"
)

// Notification copy mirrors the pre-migration frontend limits: macOS
// banners truncate long text, so titles and snippets stay short enough to
// read at a glance.
const (
	notifyTitleLimit    = 80
	notifySnippetLimit  = 160
	notifyFallbackTitle = "OpenCraft"
)

// handleDesktopNotification is installed as the core shell's notification
// sink. Every interact/turn_end/automation event still reaches the frontend
// (the UI event bus), but system notifications are now raised from Go so a
// hidden, minimized, or close-to-tray window cannot drop them.
func (d *Desktop) handleDesktopNotification(typ string, data any) {
	if d == nil || d.notifications == nil {
		return
	}
	texts := d.core.Shell.Texts()
	switch typ {
	case "interact":
		spec, ok := data.(map[string]any)
		if !ok {
			return
		}
		title, _ := spec["title"].(string)
		body := strings.TrimSpace(title)
		if body == "" {
			body = texts.NotifyInteract
		}
		d.sendNotification("interact", notifyFallbackTitle, body)
	case "turn_end":
		ev, ok := data.(core.TurnEndEvent)
		if !ok || ev.Notify != nil && !*ev.Notify {
			return
		}
		title := d.sessionTitle(ev.ConversationID)
		if title == "" {
			title = notifyFallbackTitle
		}
		title = truncateRunes(title, notifyTitleLimit)
		statusText := notifyStatus(texts, ev.Status)
		snippet := strings.TrimSpace(ev.Output)
		if snippet != "" {
			snippet = truncateRunes(snippet, notifySnippetLimit)
		}
		body := statusText
		if snippet != "" {
			body += "\n" + snippet
		}
		d.sendNotification("turn-end", title, body)
	case "automation_notify":
		payload, ok := data.(map[string]any)
		if !ok {
			return
		}
		name, _ := payload["name"].(string)
		title := strings.TrimSpace(name)
		if title == "" {
			title = notifyFallbackTitle
		}
		title = truncateRunes(title, notifyTitleLimit)
		status, _ := payload["status"].(string)
		statusText := notifyStatus(texts, status)
		output, _ := payload["output"].(string)
		errorText, _ := payload["error"].(string)
		snippet := strings.TrimSpace(output)
		if snippet == "" {
			snippet = strings.TrimSpace(errorText)
		}
		if snippet != "" {
			snippet = truncateRunes(snippet, notifySnippetLimit)
		}
		body := statusText
		if snippet != "" {
			body += "\n" + snippet
		}
		d.sendNotification("automation-turn-end", title, body)
	}
}

// sendNotification pushes one best-effort system notification.
func (d *Desktop) sendNotification(id, title, body string) {
	err := d.notifications.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: title,
		Body:  body,
	})
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: system notification failed", err,
			otellog.String("notification_id", id))
	}
}

// sessionTitle resolves the persisted conversation title for the current
// workspace Host. It falls back to "" so callers can use the app name.
func (d *Desktop) sessionTitle(contextID string) string {
	h := d.core.Runtime.Current()
	if h == nil {
		return ""
	}
	store := h.SessionsStore()
	if store == nil {
		return ""
	}
	title, err := store.Title(contextID)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: read session title for notification failed", err)
		return ""
	}
	return strings.TrimSpace(title)
}

// notifyStatus maps a terminal status onto localized notification copy.
func notifyStatus(texts core.DesktopTexts, status string) string {
	switch status {
	case "completed":
		return texts.NotifyDone
	case "failed", "aborted":
		return texts.NotifyFailed
	case "canceled":
		return texts.NotifyCancelled
	case "interrupted":
		return texts.NotifyInterrupted
	}
	return status
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
