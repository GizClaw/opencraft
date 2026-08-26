package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/hooks"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// maxViewFileSize caps the file viewer and diff payloads.
const maxViewFileSize = 1 << 20 // 1 MiB

// ListSessions returns every stored conversation, newest first.
func (a *App) ListSessions() ([]SessionMeta, error) {
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	if store == nil {
		return nil, nil
	}
	metas, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(metas))
	for _, m := range metas {
		meta := SessionMeta{
			ID:          m.ID,
			Title:       m.Title,
			CreatedAt:   m.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   m.UpdatedAt.Format(time.RFC3339),
			Messages:    m.Messages,
			TotalTokens: m.Usage.TotalTokens,
		}
		if title := a.sessionTitle(store, m.ID, m.Title); title != "" {
			meta.Title = title
		}
		out = append(out, meta)
	}
	return out, nil
}

// sessionTitle returns the user-renamed title for a conversation,
// falling back to the generated title.
func (a *App) sessionTitle(
	store *ocsessions.Store,
	id, fallback string,
) string {
	var custom string
	if store.ReadState(id, "title", &custom) == nil &&
		strings.TrimSpace(custom) != "" {
		return custom
	}
	return fallback
}

// RenameSession sets a custom display title for one conversation.
func (a *App) RenameSession(id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("title is required")
	}
	if !ocsessions.ValidID(id) {
		return fmt.Errorf("invalid session id %q", id)
	}
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	if store == nil {
		return errors.New("session store is not available")
	}
	return store.WriteState(id, "title", title)
}

// ExportSession writes one conversation's transcript to
// <workspace>/.opencraft/exports/<id>.md and returns the path. The
// markdown preserves the full timeline: reasoning traces, tool calls
// with their arguments, and tool results alongside the visible text.
func (a *App) ExportSession(id string) (string, error) {
	if !ocsessions.ValidID(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	a.mu.Lock()
	store := a.sessions
	workDir := a.workDir
	a.mu.Unlock()
	if store == nil {
		return "", errors.New("session store is not available")
	}
	msgs, err := store.History(context.Background(), id, -1)
	if err != nil {
		return "", err
	}
	title := a.sessionTitle(store, id, id)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "_%s %s_\n\n",
		time.Now().UTC().Format("2006-01-02 15:04 UTC"),
		"exported conversation",
	)
	for _, m := range msgs {
		role := "User"
		switch m.Role {
		case message.RoleAssistant:
			role = "Assistant"
		case message.RoleTool:
			role = "Tool"
		}
		fmt.Fprintf(&b, "## %s\n\n", role)
		for _, part := range m.Content.Parts {
			switch p := part.(type) {
			case message.ReasoningPart:
				if p.Text != "" {
					fmt.Fprintf(&b, "> %s\n>\n",
						strings.ReplaceAll(p.Text, "\n", "\n> "))
				}
			case message.ToolCallPart:
				args := strings.TrimSpace(string(p.Call.Arguments))
				fmt.Fprintf(&b, "**Tool call: `%s`**\n\n", p.Call.Name)
				if args != "" && args != "{}" {
					fmt.Fprintf(&b, "```json\n%s\n```\n\n", args)
				}
			case message.ToolResultPart:
				if p.Result.IsError {
					fmt.Fprintf(&b, "**Tool result (error)**\n\n")
				} else {
					fmt.Fprintf(&b, "**Tool result**\n\n")
				}
				if strings.TrimSpace(p.Result.Content) != "" {
					fmt.Fprintf(&b, "```\n%s\n```\n\n", p.Result.Content)
				}
			case message.TextPart:
				if p.Text != "" {
					fmt.Fprintf(&b, "%s\n\n", p.Text)
				}
			}
		}
	}
	dir := filepath.Join(workDir, ".opencraft", "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// CurrentSession returns the active conversation id.
func (a *App) CurrentSession() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conversationID
}

// ResumeSession switches the conversation context to an existing
// session, restoring its permission mode. It returns the session id
// so the UI can highlight it.
func (a *App) ResumeSession(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", errors.New("session id is required")
	}
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	if store == nil {
		return "", errors.New("session store is not available")
	}
	metas, err := store.List()
	if err != nil {
		return "", err
	}
	found := false
	for _, m := range metas {
		if m.ID == id {
			found = true
			break
		}
	}
	// A conversation minted by NewChat is valid even before its first
	// turn persists history/usage to the store; the in-memory index
	// knows it (and any conversation with live or past runs).
	a.mu.Lock()
	_, known := a.convRuns[id]
	a.mu.Unlock()
	if !found && !known {
		return "", fmt.Errorf("session %s not found", id)
	}
	mode, err := store.Mode(id)
	if err != nil {
		return "", err
	}
	think, err := store.Think(a.appContext(), id)
	if err != nil {
		think = ocsessions.ThinkMedium
	}
	model, err := store.Model(id)
	if err != nil {
		model = ""
	}
	a.mu.Lock()
	a.conversationID = id
	a.mode = mode
	a.think = string(think)
	a.model = model
	a.mu.Unlock()
	a.fireHooks(a.appContext(), hooks.EventSessionStart, map[string]any{
		"event":           hooks.EventSessionStart,
		"source":          "resume",
		"conversation_id": id,
	})
	return id, nil
}

// SessionHistory returns the stored messages of one conversation as
// full flowcraft messages, so resuming it restores the same ordered
// blocks (reasoning, tool calls, results, text) the live stream shows.
func (a *App) SessionHistory(id string) ([]message.Message, error) {
	if !ocsessions.ValidID(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	if store == nil {
		return nil, nil
	}
	return store.History(context.Background(), id, -1)
}

// DelegationCards snapshots the delegation kanban board, newest first.
func (a *App) DelegationCards() ([]KanbanCard, error) {
	return a.delegationCards(kanban.Filter{}, nil)
}

// ConversationDelegationCards snapshots the delegation board entries
// whose caller run belongs to one conversation. The caller run id is
// persisted on each card (async and sync), so a conversation can show
// the subagents it spawned even after the calling turn ended.
func (a *App) ConversationDelegationCards(contextID string) ([]KanbanCard, error) {
	a.mu.Lock()
	runs := a.convRuns[contextID]
	a.mu.Unlock()
	if len(runs) == 0 {
		return []KanbanCard{}, nil
	}
	return a.delegationCards(kanban.Filter{}, runs)
}

// delegationCards maps board cards to DTOs, optionally filtered to the
// caller runs of one conversation. A nil run set returns every card.
func (a *App) delegationCards(
	filter kanban.Filter,
	runs map[string]bool,
) ([]KanbanCard, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil {
		return nil, nil
	}
	value, ok := ctrl.Runtime().Resource("delegate.backend")
	if !ok {
		return nil, nil
	}
	board, ok := value.(*kanban.Board)
	if !ok {
		return nil, nil
	}
	cards := board.Query(filter)
	sort.SliceStable(cards, func(i, j int) bool {
		return cards[i].CreatedAt.After(cards[j].CreatedAt)
	})
	out := make([]KanbanCard, 0, len(cards))
	for _, c := range cards {
		if c.Task == nil {
			continue
		}
		if runs != nil {
			parent := c.Task.Request.ParentRunID
			if parent == "" {
				parent = c.Task.Request.Request.Metadata[delegation.ParentRunMetadataKey]
			}
			if !runs[parent] {
				continue
			}
		}
		card := KanbanCard{
			ID:        c.ID,
			Producer:  c.Producer,
			Consumer:  c.Consumer,
			Status:    string(c.Status),
			RunID:     c.RunID,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
			UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
		}
		if c.Task != nil {
			req := c.Task.Request.Request
			card.Target = req.Target
			card.Input = truncateDisplay(req.Input, 200)
			card.Caller = c.Task.Request.Caller
			card.Depth = c.Task.Request.Depth
			card.ParentRunID = c.Task.Request.ParentRunID
			card.CallID = c.Task.Request.CallID
			if card.ParentRunID == "" {
				card.ParentRunID = req.Metadata[delegation.ParentRunMetadataKey]
			}
			if card.CallID == "" {
				card.CallID = req.Metadata[delegation.CallIDMetadataKey]
			}
		}
		if c.Result != nil {
			card.Output = truncateDisplay(c.Result.Response.Output, 400)
			card.Error = c.Result.Response.Error
		}
		out = append(out, card)
	}
	return out, nil
}

// ReadFile returns a workspace file's content for the viewer panel.
func (a *App) ReadFile(path string) (string, error) {
	wd := a.snapshotWorkDir()
	path, err := resolveInWorkspace(wd, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxViewFileSize {
		return "", fmt.Errorf(
			"file too large to view (%d bytes)", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FileDiff returns the git diff for one workspace path (empty when the
// file is unmodified). Non-git failures surface their stderr so the UI
// can show why no diff is available.
func (a *App) FileDiff(path string) (string, error) {
	wd := a.snapshotWorkDir()
	path, err := resolveInWorkspace(wd, path)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.appContext(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, "git", "-C", wd, "diff", "--no-color", "--", path)
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("git diff timed out")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(exitErr.Stderr), nil
		}
		return "", err
	}
	return string(out), nil
}

func truncateDisplay(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
