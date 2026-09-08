package bindings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
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
	Turns       int    `json:"turns"`
	Messages    int    `json:"messages"`
	TotalTokens int64  `json:"total_tokens"`
}

func toSessionMeta(m sessions.Meta) SessionMeta {
	return SessionMeta{
		ID:          m.ID,
		Title:       m.Title,
		CreatedAt:   m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   m.UpdatedAt.UTC().Format(time.RFC3339),
		Turns:       m.Turns,
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

// SessionDeleteResult reports a deleted conversation. When the deleted
// conversation was still the workspace's current one once the removal
// settled, the backend mints its replacement in the same call and
// returns the fresh session so the UI can switch to it without a
// second request.
type SessionDeleteResult struct {
	SessionID string `json:"session_id,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Think     string `json:"think,omitempty"`
	Model     string `json:"model,omitempty"`
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
	return listStoredMetas(h.Sessions())
}

// ListInWorkspace returns stored conversation metadata for one
// workspace without switching the active runtime. The sidebar history
// tree uses it to show each workspace's sessions while it is expanded.
func (b *Session) ListInWorkspace(
	workspace string,
) ([]SessionMeta, error) {
	ctx := b.core.Shell.Context()
	mgr := b.core.Runtime.Manager()
	if mgr == nil {
		return nil, errNotReady("runtime")
	}
	layout, err := b.core.ResolveLayout(workspace)
	if err != nil {
		return nil, err
	}
	store, err := mgr.OpenSessions(ctx, workspace, layout, 40)
	if err != nil {
		return nil, err
	}
	defer mgr.ReleaseSessions(store)
	return listStoredMetas(store)
}

// listStoredMetas converts one workspace's stored conversations into
// UI summaries, overlaying a custom display title when present.
func listStoredMetas(store *sessions.Store) ([]SessionMeta, error) {
	metas, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(metas))
	for i := range metas {
		var custom string
		if store.ReadState(metas[i].ID, "title", &custom) == nil &&
			strings.TrimSpace(custom) != "" {
			metas[i].Title = custom
		}
		out = append(out, toSessionMeta(metas[i]))
	}
	return out, nil
}

func requireArchivedTurns(turns []sessions.TurnRecord) error {
	if len(turns) == 0 {
		return errors.New("session has no archived turns to export")
	}
	return nil
}

// importRequestFromTurns lowers archived turns back into the neutral
// bundle shape. Turn timestamps and the session usage ride along, so
// an exported bundle re-imports with the same worked-for durations,
// request/start anchors and token totals.
func importRequestFromTurns(
	source, title string,
	usage sessions.Usage,
	turns []sessions.TurnRecord,
) sessions.ImportRequest {
	bundle := sessions.ImportRequest{
		Source: source,
		Title:  title,
	}
	if usage.TotalTokens > 0 {
		u := usage
		bundle.Usage = &u
	}
	for _, turn := range turns {
		bundle.Turns = append(bundle.Turns, sessions.ImportTurn{
			At:          turn.At,
			RequestedAt: optionalTime(turn.RequestedAt),
			StartedAt:   optionalTime(turn.StartedAt),
			FinishedAt:  optionalTime(turn.FinishedAt),
			Messages:    turn.Messages,
		})
	}
	return bundle
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
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

// Delete removes one conversation and, when the deleted conversation
// is still the workspace's current one once the removal settles,
// atomically mints its replacement. Removing a conversation with a
// live turn cancels the run and waits for its terminal persistence
// before the rows go away, so the delete never races a running
// session's final writes.
func (b *Session) Delete(id string) (SessionDeleteResult, error) {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	// DeleteConversation is idempotent, and its lifecycle guards run
	// before any row/file removal, so a Host retirement between the UI
	// action and the delete is safe to absorb here by waiting for the
	// replacement Host and retrying inside this one RPC.
	notReady := errNotReady("session")
	deadline := time.Now().Add(startRetryWindow)
	var lastErr error
	for attempt := 0; attempt < maxStartAttempts; attempt++ {
		h := b.core.Runtime.Current()
		if h == nil || h.Sessions() == nil {
			lastErr = notReady
			if strings.TrimSpace(workDir) == "" {
				return SessionDeleteResult{}, lastErr
			}
		} else {
			err := h.DeleteConversation(ctx, id)
			if err == nil {
				b.core.Conversation.ForgetConversation(id)
				// A selection made while the delete waited (it can
				// take up to 30s to stop a live turn) wins and gets no
				// replacement.
				fresh := b.core.Conversation.ReplaceIfCurrent(workDir, id)
				if fresh == "" {
					return SessionDeleteResult{}, nil
				}
				return SessionDeleteResult{
					SessionID: fresh,
					Mode:      string(b.core.Conversation.Mode(workDir)),
					Think:     b.core.Conversation.Think(workDir),
					Model:     b.core.Conversation.Model(workDir),
				}, nil
			}
			lastErr = err
			if !host.IsRetryableStartError(lastErr) {
				return SessionDeleteResult{}, lastErr
			}
		}
		if time.Now().After(deadline) ||
			ctx.Err() != nil ||
			b.core.ActiveWorkDir() != workDir {
			return SessionDeleteResult{}, lastErr
		}
		if err := b.core.Runtime.EnsureUsableHostWithin(
			ctx, deadline, workDir, lastErr,
		); err != nil {
			return SessionDeleteResult{}, err
		}
	}
	return SessionDeleteResult{}, lastErr
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

// TurnByRunID returns the archived turn for one completed run. The
// frontend uses it to reconcile a live transcript after turn_end so
// coalesced or dropped stream deltas are replaced by the archive.
func (b *Session) TurnByRunID(
	conversationID, runID string,
) (SessionTurnDTO, error) {
	if runID == "" {
		return SessionTurnDTO{},
			fmt.Errorf("session: run id is required")
	}
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return SessionTurnDTO{}, errNotReady("session")
	}
	turn, err := h.Sessions().TurnByRunID(ctx, conversationID, runID)
	if err != nil {
		return SessionTurnDTO{}, err
	}
	return toSessionTurnDTO(turn), nil
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
	if err := requireArchivedTurns(turns); err != nil {
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
	if err := requireArchivedTurns(turns); err != nil {
		return "", err
	}
	usage, err := h.Sessions().LoadUsage(ctx, id)
	if err != nil {
		return "", err
	}
	bundle := importRequestFromTurns("opencraft:"+id, id, usage, turns)
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
// workspace store. The shared Host writes the archive, seeds memory
// and marks the conversation complete, so the UI flow and the plugin
// session.import primitive behave identically.
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
	id, err := h.ImportSession(ctx, req)
	if err != nil {
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
