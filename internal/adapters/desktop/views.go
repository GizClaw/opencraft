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

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// maxViewFileSize caps the file viewer and diff payloads.
const maxViewFileSize = 1 << 20 // 1 MiB

// ListSessions returns every stored conversation, newest first.
func (a *App) ListSessions() ([]SessionMeta, error) {
	store := a.sessionStore()
	if store == nil {
		return []SessionMeta{}, nil
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
	store := a.sessionStore()
	if store == nil {
		return errors.New("session store is not available")
	}
	return store.WriteState(id, "title", title)
}

// ExportSession writes one conversation's chat transcript to
// <workspace>/.opencraft/exports/<id>.md and returns the path. The
// markdown keeps only user and assistant turns: each archived turn's
// user messages are preserved, while assistant messages are collapsed
// to the agent's final text output for that turn. Reasoning, tool
// calls and tool results are intentionally omitted.
func (a *App) ExportSession(id string) (string, error) {
	if !ocsessions.ValidID(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	store := a.sessionStore()
	workDir := a.snapshotWorkDir()
	if store == nil {
		return "", errors.New("session store is not available")
	}
	turns, err := store.Turns(a.appContext(), id)
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
	var pendingAssistant string
	flushAssistant := func() {
		if strings.TrimSpace(pendingAssistant) == "" {
			return
		}
		fmt.Fprintf(&b, "## Assistant\n\n%s\n\n", pendingAssistant)
		pendingAssistant = ""
	}
	for _, turn := range turns {
		for _, m := range turn.Messages {
			switch m.Role {
			case message.RoleUser:
				flushAssistant()
				if text := strings.TrimSpace(m.Content.Text()); text != "" {
					fmt.Fprintf(&b, "## User\n\n%s\n\n", text)
				}
			case message.RoleAssistant:
				if text := strings.TrimSpace(m.Content.Text()); text != "" {
					// Keep the latest assistant text in this turn; an
					// earlier "I'll check" before a tool call is
					// dropped in favor of the final agent output.
					pendingAssistant = text
				}
			}
		}
		flushAssistant()
	}
	dir := a.exportsDirFor(workDir)
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
// session, restoring its settings. It returns the authoritative
// SessionSnapshot so the UI can apply id + settings atomically.
func (a *App) ResumeSession(id string) (SessionSnapshot, error) {
	if strings.TrimSpace(id) == "" {
		return SessionSnapshot{}, errors.New("session id is required")
	}
	store := a.sessionStore()
	if store == nil {
		return SessionSnapshot{}, errors.New("session store is not available")
	}
	metas, err := store.List()
	if err != nil {
		return SessionSnapshot{}, err
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
		return SessionSnapshot{}, fmt.Errorf("session %s not found", id)
	}
	mode, err := store.Mode(a.appContext(), id)
	if err != nil {
		return SessionSnapshot{}, err
	}
	think, err := store.Think(a.appContext(), id)
	if err != nil {
		think = ocsessions.ThinkMedium
	}
	model, err := store.Model(a.appContext(), id)
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
	return SessionSnapshot{
		SessionID: id,
		Mode:      string(mode),
		Think:     string(think),
		Model:     model,
	}, nil
}

// SessionHistory returns the stored messages of one conversation as
// full flowcraft messages, so resuming it restores the same ordered
// blocks (reasoning, tool calls, results, text) the live stream shows.
func (a *App) SessionHistory(id string) ([]message.Message, error) {
	if !ocsessions.ValidID(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	store := a.sessionStore()
	if store == nil {
		return []message.Message{}, nil
	}
	return store.History(a.appContext(), id, -1)
}

// SessionTurns returns every archived turn of one conversation with
// its produced artifacts, so resuming renders one artifact strip per
// turn instead of a single current-turn list.
func (a *App) SessionTurns(id string) ([]SessionTurnDTO, error) {
	if !ocsessions.ValidID(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	store := a.sessionStore()
	if store == nil {
		return []SessionTurnDTO{}, nil
	}
	turns, err := store.Turns(a.appContext(), id)
	if err != nil {
		return nil, err
	}
	out := make([]SessionTurnDTO, 0, len(turns))
	for _, t := range turns {
		out = append(out, toSessionTurnDTO(t))
	}
	return out, nil
}

// toSessionTurnDTO maps one stored turn record to the wire form,
// applying the same legacy-time fallbacks used by older archives.
func toSessionTurnDTO(t ocsessions.TurnRecord) SessionTurnDTO {
	requestedAt := t.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = t.At
	}
	startedAt := t.StartedAt
	if startedAt.IsZero() {
		startedAt = t.At
	}
	finishedAt := t.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = t.At
	}
	var durationMs int64
	if !t.StartedAt.IsZero() && !t.FinishedAt.IsZero() &&
		t.FinishedAt.After(t.StartedAt) {
		durationMs = t.FinishedAt.Sub(t.StartedAt).Milliseconds()
	}
	return SessionTurnDTO{
		Seq:         t.Seq,
		At:          t.At.Format(time.RFC3339),
		RequestedAt: requestedAt.Format(time.RFC3339),
		StartedAt:   startedAt.Format(time.RFC3339),
		FinishedAt:  finishedAt.Format(time.RFC3339),
		DurationMs:  durationMs,
		RunID:       t.RunID,
		Messages:    t.Messages,
		Artifacts:   t.Artifacts,
	}
}

// ActiveRun returns the run id currently active in one conversation.
// It covers both main-session runs and background-host runs targeting
// the currently open workspace, so a frontend reload can restore a
// busy turn without waiting for the first stream event.
func (a *App) ActiveRun(conversationID string) ActiveRunDTO {
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h != nil {
		for _, r := range h.ActiveRuns() {
			if r.ConversationID == conversationID {
				return ActiveRunDTO{RunID: r.RunID}
			}
		}
	}
	return ActiveRunDTO{}
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
	ctrl := a.controller()
	if ctrl == nil || ctrl.Runtime() == nil {
		return []KanbanCard{}, nil
	}
	value, ok := ctrl.Runtime().Resource("delegate.backend")
	if !ok {
		return []KanbanCard{}, nil
	}
	board, ok := value.(*kanban.Board)
	if !ok {
		return []KanbanCard{}, nil
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
