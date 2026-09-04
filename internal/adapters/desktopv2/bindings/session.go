package bindings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// Session exposes conversation archive/history operations.
type Session struct {
	core *core.Core
}

// NewSessionBinding wires the session binding.
func NewSessionBinding(c *core.Core) *Session {
	return &Session{core: c}
}

// SessionMeta is the UI-facing summary of one stored conversation.
type SessionMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Messages    int    `json:"messages"`
	TotalTokens int64  `json:"total_tokens"`
}

func toSessionMeta(m sessions.Meta) SessionMeta {
	return SessionMeta{
		ID:          m.ID,
		Title:       m.Title,
		CreatedAt:   m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   m.UpdatedAt.UTC().Format(time.RFC3339),
		Messages:    m.Messages,
		TotalTokens: m.Usage.TotalTokens,
	}
}

// SessionTurnDTO is the wire form of one archived turn. Times are
// RFC3339 strings and the UI duration is computed from the stored
// started/finished stamps with the same legacy fallbacks older
// archives relied on.
type SessionTurnDTO struct {
	Seq         int                 `json:"seq"`
	At          string              `json:"at"`
	RequestedAt string              `json:"requested_at,omitempty"`
	StartedAt   string              `json:"started_at,omitempty"`
	FinishedAt  string              `json:"finished_at,omitempty"`
	DurationMs  int64               `json:"duration_ms,omitempty"`
	RunID       string              `json:"run_id,omitempty"`
	Status      string              `json:"status,omitempty"`
	Error       string              `json:"error,omitempty"`
	Messages    []message.Message   `json:"messages"`
	Artifacts   []sessions.Artifact `json:"artifacts,omitempty"`
}

func toSessionTurnDTO(t sessions.TurnRecord) SessionTurnDTO {
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
		At:          t.At.UTC().Format(time.RFC3339),
		RequestedAt: requestedAt.UTC().Format(time.RFC3339),
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
		FinishedAt:  finishedAt.UTC().Format(time.RFC3339),
		DurationMs:  durationMs,
		RunID:       t.RunID,
		Status:      t.Status,
		Error:       t.Error,
		Messages:    t.Messages,
		Artifacts:   t.Artifacts,
	}
}

// List returns conversation metadata, newest first.
func (b *Session) List() ([]SessionMeta, error) {
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return []SessionMeta{}, nil
	}
	metas, err := h.Sessions().List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(metas))
	for i := range metas {
		var custom string
		if h.Sessions().ReadState(metas[i].ID, "title", &custom) == nil &&
			strings.TrimSpace(custom) != "" {
			metas[i].Title = custom
		}
		out = append(out, toSessionMeta(metas[i]))
	}
	return out, nil
}

// History returns the most recent n archived messages of a session.
func (b *Session) History(
	id string, n int,
) ([]message.Message, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return []message.Message{}, nil
	}
	return h.Sessions().History(ctx, id, n)
}

// Exists reports whether a conversation exists.
func (b *Session) Exists(id string) bool {
	h := b.core.Runtime.Current()
	return h != nil && h.Sessions() != nil && h.Sessions().Exists(id)
}

// Rename sets a custom display title for one conversation.
func (b *Session) Rename(id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("title is required")
	}
	if !sessions.ValidID(id) {
		return fmt.Errorf("invalid session id %q", id)
	}
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return errNotReady("session")
	}
	return h.Sessions().WriteState(id, "title", title)
}

// Delete removes one conversation. The active conversation is refused.
func (b *Session) Delete(id string) error {
	ctx := b.core.Shell.Context()
	if id == b.core.Conversation.Current() {
		return errors.New("cannot delete the active conversation")
	}
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return errNotReady("session")
	}
	if err := h.Sessions().Remove(ctx, id); err != nil {
		return err
	}
	b.core.Conversation.ForgetConversation(id)
	return nil
}

// Turns returns every archived turn of one conversation.
func (b *Session) Turns(
	id string,
) ([]SessionTurnDTO, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return nil, errNotReady("session")
	}
	turns, err := h.Sessions().Turns(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]SessionTurnDTO, 0, len(turns))
	for _, turn := range turns {
		out = append(out, toSessionTurnDTO(turn))
	}
	return out, nil
}

func (b *Session) exportsDir() (string, error) {
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return "", errors.New("session: no workspace selected")
	}
	layout, err := b.core.ResolveLayout(workDir)
	if err != nil {
		return "", err
	}
	return layout.ExportsDir, nil
}

// ExportMarkdown writes a human-readable transcript and returns path.
func (b *Session) ExportMarkdown(
	id string,
) (string, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return "", errNotReady("session")
	}
	turns, err := h.Sessions().Turns(ctx, id)
	if err != nil {
		return "", err
	}
	var bld strings.Builder
	fmt.Fprintf(&bld, "# %s\n\n", id)
	var pending string
	flush := func() {
		if strings.TrimSpace(pending) == "" {
			return
		}
		fmt.Fprintf(&bld, "## Assistant\n\n%s\n\n", pending)
		pending = ""
	}
	for _, turn := range turns {
		for _, m := range turn.Messages {
			switch m.Role {
			case message.RoleUser:
				flush()
				if text := strings.TrimSpace(m.Content.Text()); text != "" {
					fmt.Fprintf(&bld, "## User\n\n%s\n\n", text)
				}
			case message.RoleAssistant:
				if text := strings.TrimSpace(m.Content.Text()); text != "" {
					pending = text
				}
			}
		}
		flush()
	}
	dir, err := b.exportsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".md")
	return path, os.WriteFile(path, []byte(bld.String()), 0o644)
}

// ExportBundle writes a neutral JSON session bundle.
func (b *Session) ExportBundle(
	id string,
) (string, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return "", errNotReady("session")
	}
	turns, err := h.Sessions().Turns(ctx, id)
	if err != nil {
		return "", err
	}
	bundle := sessions.ImportRequest{
		Source: "opencraft:" + id,
		Title:  id,
	}
	for _, turn := range turns {
		bundle.Turns = append(bundle.Turns, sessions.ImportTurn{
			At:       turn.At,
			Messages: turn.Messages,
		})
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	dir, err := b.exportsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".json")
	return path, os.WriteFile(path, data, 0o644)
}

// SessionImportDTO reports a completed bundle import to the UI.
type SessionImportDTO struct {
	SessionID string `json:"session_id"`
	Messages  int    `json:"messages"`
	Turns     int    `json:"turns"`
}

// ImportBundle imports a neutral session bundle into the current
// workspace store.
func (b *Session) ImportBundle(
	path string,
) (SessionImportDTO, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return SessionImportDTO{}, errNotReady("session")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionImportDTO{}, err
	}
	var req sessions.ImportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return SessionImportDTO{}, err
	}
	if req.Source == "" {
		req.Source = fmt.Sprintf("opencraft:%d", time.Now().UnixNano())
	}
	id, err := h.Sessions().Import(ctx, req)
	if err != nil {
		return SessionImportDTO{}, err
	}
	// The desktop import flow has no separate memory-seeding stage:
	// mark the conversation complete immediately so List surfaces it
	// in the session sidebar instead of leaving it pending forever.
	if err := h.Sessions().CompleteImport(ctx, id); err != nil {
		telemetry.WarnErr(ctx, "desktop session: abort failed import failed",
			h.Sessions().AbortImport(ctx, id))
		return SessionImportDTO{}, err
	}
	messages := 0
	for _, turn := range req.Turns {
		messages += len(turn.Messages)
	}
	return SessionImportDTO{
		SessionID: id,
		Messages:  messages,
		Turns:     len(req.Turns),
	}, nil
}

// ActiveRun returns the run id currently active in one conversation.
func (b *Session) ActiveRun(conversationID string) string {
	h := b.core.Runtime.Current()
	if h == nil {
		return ""
	}
	for _, r := range h.ActiveRuns() {
		if r.ConversationID == conversationID {
			return r.RunID
		}
	}
	return ""
}

// DelegationCard is one delegation board entry.
type DelegationCard struct {
	ID          string `json:"id"`
	Producer    string `json:"producer,omitempty"`
	Consumer    string `json:"consumer,omitempty"`
	Status      string `json:"status"`
	Target      string `json:"target"`
	Input       string `json:"input,omitempty"`
	Output      string `json:"output,omitempty"`
	Caller      string `json:"caller,omitempty"`
	Depth       int    `json:"depth"`
	Error       string `json:"error,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
	CallID      string `json:"call_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func cardDTO(c *kanban.Card) (DelegationCard, bool) {
	if c == nil || c.Task == nil {
		return DelegationCard{}, false
	}
	req := c.Task.Request.Request
	dto := DelegationCard{
		ID:        c.ID,
		Producer:  c.Producer,
		Consumer:  c.Consumer,
		Status:    string(c.Status),
		RunID:     c.RunID,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
	}
	dto.Target = req.Target
	dto.Input = truncateDisplay(req.Input, 200)
	dto.Caller = c.Task.Request.Caller
	dto.Depth = c.Task.Request.Depth
	dto.ParentRunID = c.Task.Request.ParentRunID
	dto.CallID = c.Task.Request.CallID
	if dto.ParentRunID == "" {
		dto.ParentRunID = req.Metadata[delegation.ParentRunMetadataKey]
	}
	if dto.CallID == "" {
		dto.CallID = req.Metadata[delegation.CallIDMetadataKey]
	}
	if c.Result != nil {
		dto.Output = truncateDisplay(c.Result.Response.Output, 400)
		dto.Error = c.Result.Response.Error
	}
	return dto, true
}

func sortCardsNewestFirst(cards []DelegationCard) {
	sort.SliceStable(cards, func(i, j int) bool {
		return cards[i].CreatedAt > cards[j].CreatedAt
	})
}

func truncateDisplay(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (b *Session) board() (*kanban.Board, error) {
	h := b.core.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return nil, errNotReady("delegation")
	}
	value, ok := h.Controller().Runtime().Resource("delegate.backend")
	if !ok {
		return nil, errNotReady("delegation")
	}
	board, ok := value.(*kanban.Board)
	if !ok {
		return nil, errNotReady("delegation")
	}
	return board, nil
}

// DelegationCards snapshots the delegation board.
func (b *Session) DelegationCards() ([]DelegationCard, error) {
	board, err := b.board()
	if err != nil {
		return []DelegationCard{}, nil
	}
	out := make([]DelegationCard, 0)
	for _, c := range board.Query(kanban.Filter{}) {
		if dto, ok := cardDTO(c); ok {
			out = append(out, dto)
		}
	}
	sortCardsNewestFirst(out)
	return out, nil
}

// ConversationDelegationCards snapshots cards owned by one
// conversation's caller runs.
func (b *Session) ConversationDelegationCards(
	conversationID string,
) ([]DelegationCard, error) {
	runs := b.core.Conversation.Runs(conversationID)
	if len(runs) == 0 {
		return []DelegationCard{}, nil
	}
	board, err := b.board()
	if err != nil {
		return []DelegationCard{}, nil
	}
	out := make([]DelegationCard, 0)
	for _, c := range board.Query(kanban.Filter{}) {
		dto, ok := cardDTO(c)
		if !ok {
			continue
		}
		if runs[dto.ParentRunID] {
			out = append(out, dto)
		}
	}
	sortCardsNewestFirst(out)
	return out, nil
}
