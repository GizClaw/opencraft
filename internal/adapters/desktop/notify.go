package desktop

import (
	"context"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/orchestration/host"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"go.opentelemetry.io/otel/attribute"
)

// Notification copy mirrors the pre-migration frontend limits: macOS
// banners truncate long text, so titles and snippets stay short enough to
// read at a glance.
const (
	notifyTitleLimit    = 80
	notifySnippetLimit  = 160
	notifyFallbackTitle = "OpenCraft"
)

// Banner kinds, the namespace half of a banner id. One conversation
// keeps one banner per kind: a second turn ending replaces its own
// banner and leaves the input request standing next to it.
const (
	bannerInteract   = "interact"
	bannerTurnEnd    = "turn-end"
	bannerAutomation = "automation"
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
		conversationID, _ := spec["conversation_id"].(string)
		workspace, _ := spec["workspace"].(string)
		title, _ := spec["title"].(string)
		body := strings.TrimSpace(title)
		if body == "" {
			body = texts.NotifyInteract
		}
		d.sendNotification(
			bannerID(bannerInteract, conversationID),
			notifyFallbackTitle, body,
			openConversationData(conversationID, workspace),
		)
	case core.NotifyTurnEnd:
		ev, ok := data.(core.TurnEndEvent)
		if !ok || ev.Notify != nil && !*ev.Notify {
			return
		}
		title := d.sessionTitle(ev.Workspace, ev.ConversationID)
		if title == "" {
			title = notifyFallbackTitle
		}
		title = truncateRunes(title, notifyTitleLimit)
		statusText := notifyStatus(texts, ev.Status, ev.ErrorKind)
		snippet := strings.TrimSpace(ev.Output)
		if snippet != "" {
			snippet = truncateRunes(snippet, notifySnippetLimit)
		}
		body := statusText
		if snippet != "" {
			body += "\n" + snippet
		}
		d.sendNotification(
			bannerID(bannerTurnEnd, ev.ConversationID),
			title, body,
			openConversationData(ev.ConversationID, ev.Workspace),
		)
	case core.NotifyAutomation:
		payload, ok := data.(map[string]any)
		if !ok {
			return
		}
		taskID, _ := payload["task_id"].(string)
		conversationID, _ := payload["conversation_id"].(string)
		workspace, _ := payload["workspace"].(string)
		title, body := automationNotification(texts, payload)
		d.sendNotification(
			bannerID(bannerAutomation, taskID),
			title, body,
			openConversationData(conversationID, workspace),
		)
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
	body = notifyStatus(texts, status, "")
	if snippet != "" {
		body += "\n" + truncateRunes(snippet, notifySnippetLimit)
	}
	return title, body
}

// sendNotification pushes one best-effort system notification. data
// travels with the banner so activating it can name the conversation and
// the workspace that owns it; the platform hands the map back untouched
// in NotificationResponse.UserInfo.
func (d *Desktop) sendNotification(id, title, body string, data map[string]any) {
	err := d.notifications.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: title,
		Body:  body,
		Data:  data,
	})
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: system notification failed", err,
			attribute.String("notification_id", id))
	}
}

// bannerID names one banner. The id is the replacement key — macOS and
// Linux replace a banner still in the notification center, Windows
// re-raises by id — so a second result for the same conversation updates
// its banner instead of stacking a column of them. A payload without a
// conversation falls back to the bare kind, which still replaces the
// previous untargeted banner of that kind.
func bannerID(kind, key string) string {
	if key == "" {
		return kind
	}
	return kind + ":" + key
}

// openConversationData is what a banner carries so that activating it can
// reopen its conversation. Empty values are dropped rather than sent as
// empty strings: a click then opens nothing instead of switching to a
// conversation that is not there.
func openConversationData(conversationID, workspace string) map[string]any {
	data := map[string]any{}
	if conversationID != "" {
		data["conversation_id"] = conversationID
	}
	if workspace != "" {
		data["workspace"] = workspace
	}
	return data
}

// handleNotificationResponse reopens the conversation a banner belongs to
// when the user activates it. It runs on the platform's notification
// thread — macOS keeps its completion handler waiting until this
// returns — so the window work moves to a goroutine of its own.
func (d *Desktop) handleNotificationResponse(result notifications.NotificationResult) {
	if result.Error != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: notification response unusable", result.Error)
		return
	}
	ev, ok := notificationOpenTarget(
		result.Response.ActionIdentifier, result.Response.UserInfo)
	if !ok {
		return
	}
	go func() {
		d.core.Shell.FocusMain()
		d.core.Shell.Emit(core.EventOpenConversation, ev)
	}()
}

// notificationOpenTarget maps an activated banner onto the conversation
// to open. Only the default action counts — a dismissal, or a button a
// future build adds, means something else — and only a banner that named
// a conversation has a target. The payload crosses JSON on every
// platform, so values are read with a string assertion: an unexpected
// type is a banner without a target, not a panic on the notification
// thread.
func notificationOpenTarget(
	actionID string, data map[string]any,
) (core.OpenConversationEvent, bool) {
	if actionID != notifications.DefaultActionIdentifier {
		return core.OpenConversationEvent{}, false
	}
	conversationID, _ := data["conversation_id"].(string)
	if conversationID == "" {
		return core.OpenConversationEvent{}, false
	}
	workspace, _ := data["workspace"].(string)
	return core.OpenConversationEvent{
		ConversationID: conversationID,
		Workspace:      workspace,
	}, true
}

// sessionTitle resolves the persisted conversation title for the Host
// that owns workspace, falling back to the window's own. A turn that
// finishes in a background workspace has no title on the active Host, so
// asking it first would put the app name on every background banner.
func (d *Desktop) sessionTitle(workspace, contextID string) string {
	h := d.core.Runtime.HostFor(workspace)
	if h == nil {
		h = d.core.ActiveHost()
	}
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
		// The pre-check above is not atomic: a runtime rebuild or
		// shutdown closes the store between the two calls, and the
		// title read then fails with a closed-database driver error.
		// That race is expected copy-wise — the banner falls back to
		// the app name — so only a still-open store reports the error.
		if store.Closed() {
			return ""
		}
		telemetry.WarnErr(context.Background(),
			"desktop: read session title for notification failed", err)
		return ""
	}
	return strings.TrimSpace(title)
}

// notifyStatus maps a terminal status onto localized notification copy.
// errorKind keeps a deadline out of the cancelled banner: the engine
// reports a timeout and a user stop as the same status, and only the
// structured class tells them apart.
func notifyStatus(texts core.DesktopTexts, status, errorKind string) string {
	switch status {
	case "completed":
		return texts.NotifyDone
	case "failed", "aborted":
		return texts.NotifyFailed
	case "canceled":
		if errorKind == host.ErrorKindTimeout {
			return texts.NotifyTimeout
		}
		return texts.NotifyCancelled
	case "timeout":
		// An automation run cut off at the task's own timeout: its
		// record carries the status apart from failed/canceled, so the
		// banner names it instead of leaking the enum.
		return texts.NotifyTimeout
	case "interrupted":
		return texts.NotifyInterrupted
	}
	// An unknown status is a status this build cannot name: a banner is
	// user-facing copy, so it says the generic thing instead of printing
	// the enum that arrived, and the log keeps the value for whoever has
	// to widen the switch.
	telemetry.WarnErr(context.Background(),
		"desktop: unknown turn status for notification",
		fmt.Errorf("status %q", status))
	return texts.NotifyFailed
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
