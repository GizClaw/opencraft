package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/message"

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
		out = append(out, SessionMeta{
			ID:          m.ID,
			Title:       m.Title,
			CreatedAt:   m.CreatedAt,
			UpdatedAt:   m.UpdatedAt,
			Messages:    m.Messages,
			TotalTokens: m.Usage.TotalTokens,
		})
	}
	return out, nil
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
	if !found {
		return "", fmt.Errorf("session %s not found", id)
	}
	mode, err := store.Mode(id)
	if err != nil {
		return "", err
	}
	think, err := store.Think(id)
	if err != nil {
		think = ocsessions.ThinkMedium
	}
	a.mu.Lock()
	a.conversationID = id
	a.mode = mode
	a.think = string(think)
	a.mu.Unlock()
	return id, nil
}

// SessionHistory returns the stored messages of one conversation so
// resuming it can restore the visible transcript.
func (a *App) SessionHistory(id string) ([]HistoryMsg, error) {
	a.mu.Lock()
	store := a.sessions
	a.mu.Unlock()
	if store == nil {
		return nil, nil
	}
	msgs, err := store.History(context.Background(), id, 0)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryMsg, 0, len(msgs))
	for _, m := range msgs {
		var text string
		for _, p := range m.Content.Parts {
			if tp, ok := p.(message.TextPart); ok {
				text += tp.Text
			}
		}
		out = append(out, HistoryMsg{Role: string(m.Role), Text: text})
	}
	return out, nil
}

// DelegationCards snapshots the delegation kanban board, newest first.
func (a *App) DelegationCards() ([]KanbanCard, error) {
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
	cards := board.Query(kanban.Filter{})
	sort.SliceStable(cards, func(i, j int) bool {
		return cards[i].CreatedAt.After(cards[j].CreatedAt)
	})
	out := make([]KanbanCard, 0, len(cards))
	for _, c := range cards {
		card := KanbanCard{
			ID:        c.ID,
			Producer:  c.Producer,
			Consumer:  c.Consumer,
			Status:    string(c.Status),
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		}
		if c.Task != nil {
			req := c.Task.Request.Request
			card.Target = req.Target
			card.Input = truncateDisplay(req.Input, 200)
			card.Caller = c.Task.Request.Caller
			card.Depth = c.Task.Request.Depth
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
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
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
	ctx, cancel := context.WithTimeout(a.appContext(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, "git", "-C", a.workDir, "diff", "--no-color", "--", path)
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
