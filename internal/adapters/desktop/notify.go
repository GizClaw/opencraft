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
// sink. interact/turn_end reach it as UI events that also notify; the
// automation result reaches it through Shell.Notify because the
// frontend has no consumer for that payload. Either way the banner is
// raised from Go, so a hidden, minimized, or close-to-tray window
// cannot drop it.
func (d *Desktop) handleDesktopNotification(typ string, data any) {
	if d == nil || d.notifications == nil {
		return
	}
	texts := d.core.Shell.Texts()
	switch typ {
	case core.NotifyInteract:
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
	case core.NotifyTurnEnd:
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
	case core.NotifyAutomation:
		payload, ok := data.(map[string]any)
		if !ok {
			return
		}
		title, body := automationNotification(texts, payload)
		d.sendNotification("automation-turn-end", title, body)
	}
}

// automationNotification shapes one automation result into the title and
// body of its banner: the task name, then the status line, then whatever
// the task produced (its output, or its error when it produced none).
func automationNotification(
	texts core.DesktopTexts, payload map[string]any,
) (title, body string) {
	name, _ := payload["name"].(string)
	title = strings.TrimSpace(name)
	if title == "" {
		title = notifyFallbackTitle
	}
	title = truncateRunes(title, notifyTitleLimit)
	status, _ := payload["status"].(string)
	output, _ := payload["output"].(string)
	errorText, _ := payload["error"].(string)
	snippet := strings.TrimSpace(output)
	if snippet == "" {
		snippet = strings.TrimSpace(errorText)
	}
	body = notifyStatus(texts, status)
	if snippet != "" {
		body += "\n" + truncateRunes(snippet, notifySnippetLimit)
	}
	return title, body
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
	// The banner is best-effort copy: during a reload or shutdown the store
	// is already closed, and a missing title is not worth a driver error in
	// the log. The caller falls back to the app name.
	if store.Closed() {
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
