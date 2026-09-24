package bindings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
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
// RFC3339 strings, resolved once by the store (TurnRecord.Timing):
// a timestamp the archive never recorded displays as the turn's own
// `at`, and duration_ms is measured from the recorded start/finish
// pair.
type SessionTurnDTO struct {
	Seq         int    `json:"seq"`
	At          string `json:"at"`
	RequestedAt string `json:"requested_at,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
	// InterruptCause / ErrorKind are the structured class of a failed
	// turn, so the transcript renders the same copy as the live turn
	// without parsing Error.
	InterruptCause string `json:"interrupt_cause,omitempty"`
	ErrorKind      string `json:"error_kind,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	ResponseID     string `json:"response_id,omitempty"`
	// Kind names who wrote a turn the app itself archived (a
	// delegation note); Note is that turn's decoded record. Ordinary
	// turns carry neither, and a payload that does not decode leaves
	// Note nil — the transcript then renders the row's text instead of
	// the card, which loses nothing.
	Kind      string              `json:"kind,omitempty"`
	Note      *DelegationNoteDTO  `json:"delegation_note,omitempty"`
	Messages  []message.Message   `json:"messages"`
	Artifacts []sessions.Artifact `json:"artifacts,omitempty"`
}

// DelegationNoteDTO is the structured record of one delegation note
// turn: a finished subagent's report as the app filed it, so the
// transcript renders the card without parsing the note's prose.
type DelegationNoteDTO struct {
	// Target is the subagent that produced the report.
	Target string `json:"target"`
	// Status is its terminal delegation status.
	Status string `json:"status"`
	// CardID / RunID / ParentRunID name the delegation on the board.
	CardID      string `json:"card_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
	// Body is the quoted answer (or failure), the same excerpt the
	// note carries for the model.
	Body string `json:"body,omitempty"`
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

func toSessionTurnDTO(
	ctx context.Context, conversationID string, t sessions.TurnRecord,
) SessionTurnDTO {
	timing := t.Timing()
	return SessionTurnDTO{
		Seq:            t.Seq,
		At:             timing.At.UTC().Format(time.RFC3339),
		RequestedAt:    timing.RequestedAt.UTC().Format(time.RFC3339),
		StartedAt:      timing.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:     timing.FinishedAt.UTC().Format(time.RFC3339),
		DurationMs:     timing.Duration.Milliseconds(),
		RunID:          t.RunID,
		Status:         t.Status,
		Error:          t.Error,
		InterruptCause: t.InterruptCause,
		ErrorKind:      t.ErrorKind,
		RequestID:      t.RequestID,
		ResponseID:     t.ResponseID,
		Kind:           t.Kind,
		Note:           delegationNoteDTO(ctx, conversationID, t),
		Messages:       t.Messages,
		Artifacts:      t.Artifacts,
	}
}

// delegationNoteDTO decodes a note turn's payload. Only the kind this
// build knows is decoded; anything else stays a plain turn, so a stored
// kind from a newer build is carried through (Kind) without being
// guessed at. A payload that does not decode is dropped with a warning
// rather than failing the read: the rows that do load must not depend
// on one row the app itself wrote badly.
func delegationNoteDTO(
	ctx context.Context, conversationID string, t sessions.TurnRecord,
) *DelegationNoteDTO {
	if t.Kind != subagents.KindDelegationNote || len(t.Payload) == 0 {
		return nil
	}
	var payload subagents.NotePayload
	if err := json.Unmarshal(t.Payload, &payload); err != nil {
		telemetry.WarnErr(ctx, "session: decode delegation note payload failed",
			err,
			otellog.String("conversation.id", conversationID),
			otellog.String("kind", t.Kind))
		return nil
	}
	return &DelegationNoteDTO{
		Target:      payload.Target,
		Status:      payload.Status,
		CardID:      payload.CardID,
		RunID:       payload.RunID,
		ParentRunID: payload.ParentRunID,
		Body:        payload.Body,
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
		// The title document is the cosmetic kind (see
		// sessions.Store.ReadState): a title that cannot be read falls
		// back to the conversations.title column rather than failing the
		// whole listing.
		if store.ReadState(metas[i].ID, sessions.DocumentTitle, &custom) == nil &&
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
			Kind:        turn.Kind,
			Payload:     turn.Payload,
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
	if !ids.IsSession(id) {
		return fmt.Errorf("invalid session id %q", id)
	}
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return errNotReady("session")
	}
	return h.Sessions().WriteState(id, sessions.DocumentTitle, title)
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
	limit int,
	beforeSeq int64,
) ([]SessionTurnDTO, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return nil, errNotReady("session")
	}
	turns, err := h.Sessions().TurnsPage(ctx, id, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	out := make([]SessionTurnDTO, 0, len(turns))
	for _, turn := range turns {
		out = append(out, toSessionTurnDTO(ctx, id, turn))
	}
	return out, nil
}

// TurnsSince returns the archived turns appended after one sequence
// number. A transcript that is already on screen uses it to pick up a
// turn the app wrote on its own — a delegation note is appended when
// its subagent finishes, which can be long after the turn that spawned
// it ended — without re-reading the turns the client already holds.
func (b *Session) TurnsSince(
	id string,
	afterSeq int64,
	limit int,
) ([]SessionTurnDTO, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return nil, errNotReady("session")
	}
	turns, err := h.Sessions().TurnsSince(ctx, id, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SessionTurnDTO, 0, len(turns))
	for _, turn := range turns {
		out = append(out, toSessionTurnDTO(ctx, id, turn))
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
	return toSessionTurnDTO(ctx, conversationID, turn), nil
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

// delegationNoteHeading labels a note turn in the exported markdown:
// the subagent and how it ended when the payload decodes, and a plain
// heading otherwise — a note whose fields are missing is still the app
// reporting, never "User".
func delegationNoteHeading(
	ctx context.Context, conversationID string, turn sessions.TurnRecord,
) string {
	note := delegationNoteDTO(ctx, conversationID, turn)
	if note == nil {
		return "Delegated result"
	}
	heading := "Delegated result: " + note.Target
	if note.Status != "" {
		heading += " (" + note.Status + ")"
	}
	return heading
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
		// A turn the app wrote (a delegation note) is not the user
		// speaking: the exported transcript labels it as what it is
		// instead of putting the app's words in the user's mouth.
		userHeading := "User"
		if turn.Kind == subagents.KindDelegationNote {
			userHeading = delegationNoteHeading(ctx, id, turn)
		}
		for _, m := range turn.Messages {
			switch m.Role {
			case message.RoleUser:
				flush()
				if text := strings.TrimSpace(m.Content.Text()); text != "" {
					fmt.Fprintf(&bld, "## %s\n\n%s\n\n", userHeading, text)
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

// ProcessView is the UI-facing snapshot of one sandboxed child process
// of a conversation: what the sandbox started, and the bounded tail of
// its output. Reads are additive — polling returns the same tail plus
// whatever arrived since, and Seq only moves when output did, so a
// poller can skip untouched rows.
type ProcessView struct {
	// ProcessID is the sandbox session id (exec_session's process_id).
	ProcessID string   `json:"process_id"`
	Argv      []string `json:"argv"`
	Workdir   string   `json:"workdir,omitempty"`
	TTY       bool     `json:"tty"`
	PID       int      `json:"pid"`
	StartedAt string   `json:"started_at"`
	Running   bool     `json:"running"`
	// ExitCode is set once the process exited normally. A killed,
	// released, or evicted process reports no code.
	ExitCode   *int   `json:"exit_code,omitempty"`
	ExitReason string `json:"exit_reason,omitempty"`
	// Tail is the merged stdout/stderr/pty suffix, at most 16 KiB.
	Tail string `json:"tail"`
	// Truncated reports that the tail is not the whole output (the
	// process printed more, or the backend dropped bytes the feed had
	// not read yet).
	Truncated bool  `json:"truncated"`
	Seq       int64 `json:"seq"`
}

// Processes returns the sandboxed processes the given conversation
// started on the current runtime, oldest first. The running processes
// run in the sandbox, not in the app; the list is the read-only view
// the activity card polls.
func (b *Session) Processes(conversationID string) []ProcessView {
	h := b.core.Runtime.Current()
	if h == nil {
		return []ProcessView{}
	}
	procs := h.Processes(conversationID)
	out := make([]ProcessView, 0, len(procs))
	for _, p := range procs {
		out = append(out, toProcessView(p))
	}
	return out
}

func toProcessView(p sandbox.Process) ProcessView {
	return ProcessView{
		ProcessID:  p.ID,
		Argv:       append([]string{}, p.Argv...),
		Workdir:    p.Workdir,
		TTY:        p.TTY,
		PID:        p.PID,
		StartedAt:  p.StartedAt.UTC().Format(time.RFC3339),
		Running:    p.Running,
		ExitCode:   p.ExitCode,
		ExitReason: p.ExitReason,
		Tail:       p.Tail,
		Truncated:  p.Truncated,
		Seq:        p.Seq,
	}
}
