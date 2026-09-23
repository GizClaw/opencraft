// Package sessions implements opencraft's project-scoped conversation
// store. SQLite owns conversation transcripts and metadata; the
// per-session directories only hold attachments and the rollout audit
// stream.
package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/foundation/utils/imageutil"
)

// ResourceKind is the deploy resource kind implemented by this package.
const ResourceKind = "session.Store"

// Artifact is one file produced by a turn.
type Artifact struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes,omitempty"`
}

// Usage is the cumulative token usage recorded for one session.
type Usage struct {
	Model            string `json:"model,omitempty"`
	InputTokens      int64  `json:"input_tokens,omitempty"`
	OutputTokens     int64  `json:"output_tokens,omitempty"`
	TotalTokens      int64  `json:"total_tokens,omitempty"`
	CacheReadTokens  int64  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64  `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64  `json:"reasoning_tokens,omitempty"`
	LatencyMs        int64  `json:"latency_ms,omitempty"`
	Calls            int64  `json:"calls,omitempty"`
}

// NormalizeModelName reduces a recorded model key to its name only.
// Legacy rows and exported bundles keyed usage by "provider/name";
// user-level statistics intentionally bucket by model name across
// providers, so any prefix up to the first "/" is dropped. Every
// write and read of persisted usage goes through this rule so imports
// of pre-normalization bundles cannot reintroduce provider-prefixed
// keys.
func NormalizeModelName(model string) string {
	if i := strings.IndexByte(model, '/'); i >= 0 {
		return model[i+1:]
	}
	return model
}

// TurnRecord is one archived turn.
type TurnRecord struct {
	Seq         int       `json:"seq"`
	At          time.Time `json:"at"`
	RequestedAt time.Time `json:"requested_at,omitzero"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
	RunID       string    `json:"run_id,omitempty"`
	Status      string    `json:"status,omitempty"`
	Error       string    `json:"error,omitempty"`
	// InterruptCause / ErrorKind are the structured class of a failed
	// turn. They are the fields a UI renders from; Error stays for
	// correlation with provider-side logs.
	InterruptCause string `json:"interrupt_cause,omitempty"`
	ErrorKind      string `json:"error_kind,omitempty"`
	// RequestID is the provider request identifier of the terminal
	// operation, surfaced in the UI warning box for correlation with
	// provider-side logs. Empty when the provider reported none.
	RequestID string `json:"request_id,omitempty"`
	// ResponseID is the provider response identifier (chat/message
	// id). It is usually only known once a response started, so
	// successful turns may carry it while request id stays empty.
	ResponseID string            `json:"response_id,omitempty"`
	Messages   []message.Message `json:"messages"`
	Artifacts  []Artifact        `json:"artifacts,omitempty"`
	// Kind names the author of a turn the app itself wrote (a
	// delegation note, for example); empty for every turn a user or
	// model produced. Payload is that author's own structured record,
	// decoded by whoever knows the kind — the store only carries it.
	Kind    string          `json:"kind,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// TurnTiming carries the timestamps one turn should display.
type TurnTiming struct {
	RequestedAt time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
}

// Meta describes one stored conversation for the /resume list.
type Meta struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Turns     int
	Messages  int
	Usage     Usage
}

// Store is the conversation store. SQLite owns transcript, metadata,
// memory rows, checkpoints and settings; the on-disk session dirs only
// hold attachments and rollout logs. It is safe for concurrent use.
type Store struct {
	root   string
	window int
	db     *state.Store

	// closed flips when the database handle closes. Best-effort readers
	// (a desktop notification raised while the app shuts down) check it
	// instead of reporting a driver error that says nothing actionable.
	closed atomic.Bool

	mu          sync.Mutex
	artifactBuf map[string][]Artifact
	turnTiming  map[string]map[string]TurnTiming
}

// New creates a Store rooted at root. The window is a convenience
// default for History(0); model context is owned by the memory layer.
// New opens the SQLite handle but does not migrate it: the workspace
// caller must run internal/foundation/compat.Workspace before use.
func New(root string, window int) (*Store, error) {
	if root == "" {
		return nil, errdefs.Validationf("session store: root is required")
	}
	if window <= 0 {
		window = 40
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	telemetry.WarnErr(context.Background(),
		"sessions: secure store root failed", os.Chmod(root, 0o700))
	db, err := state.Open(filepath.Join(root, "session.db"))
	if err != nil {
		return nil, err
	}
	s := &Store{
		root:        root,
		window:      window,
		db:          db,
		artifactBuf: make(map[string][]Artifact),
		turnTiming:  make(map[string]map[string]TurnTiming),
	}
	return s, nil
}

// State returns the SQLite store backing this session store.
func (s *Store) State() *state.Store { return s.db }

// Database returns the shared workspace DB handle.
func (s *Store) Database() *db.DB { return s.db.Handle() }

// CloseDB closes the SQLite database handle. Store intentionally does
// not implement io.Closer: one workspace Store is shared by every
// flowcraft runtime, so runtimes must not own its close. The
// orchestration/host Manager owns the DB lifecycle and calls CloseDB
// only after the last Host/store reference is released.
func (s *Store) CloseDB() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.closed.Store(true)
	return s.db.Close()
}

// Closed reports whether the store's database handle has been closed. A nil
// store reads as closed: it cannot serve reads either.
func (s *Store) Closed() bool {
	return s == nil || s.closed.Load()
}

// Save implements agent.CheckpointStore.
func (s *Store) Save(ctx context.Context, cp agent.Checkpoint) error {
	return s.db.Save(ctx, cp)
}

// Load implements agent.CheckpointStore.
func (s *Store) Load(ctx context.Context, execID string) (*agent.Checkpoint, error) {
	return s.db.Load(ctx, execID)
}

// Delete implements agent.CheckpointDeleter.
func (s *Store) Delete(ctx context.Context, execID string) error {
	return s.db.Delete(ctx, execID)
}

var _ agent.CheckpointStore = (*Store)(nil)
var _ agent.CheckpointDeleter = (*Store)(nil)

// NewID returns a fresh random session id.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "s-" + hex.EncodeToString(b[:])
}

// ValidID reports whether id is a safe conversation id.
func ValidID(id string) bool {
	if id == "" || !strings.HasPrefix(id, "s-") {
		return false
	}
	return !strings.ContainsAny(id, `/\`)
}

// DefaultSessionID is the stable session key used by tools that run
// outside any conversation.
const DefaultSessionID = "s-default"

// Exists reports whether the conversation has a state row or an
// on-disk session directory.
func (s *Store) Exists(id string) bool {
	if err := requireID(id); err != nil {
		return false
	}
	if _, err := s.db.Conversation(context.Background(), id); err == nil {
		return true
	}
	info, err := os.Stat(s.dir(id))
	return err == nil && info.IsDir()
}

// Create makes a fresh conversation and returns its id.
func (s *Store) Create() (string, error) {
	id := NewID()
	if err := s.db.EnsureConversation(context.Background(), state.Conversation{
		ID: id,
	}); err != nil {
		return "", err
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	telemetry.WarnErr(context.Background(),
		"sessions: secure conversation dir failed", os.Chmod(dir, 0o700))
	return id, nil
}

// AppendTurn persists one turn without a run id.
func (s *Store) AppendTurn(ctx context.Context, id string, msgs []message.Message) error {
	return s.appendTurn(ctx, id, "", TurnOrigin{}, msgs)
}

// AppendTurnWithRunID persists one turn with a run id.
func (s *Store) AppendTurnWithRunID(
	ctx context.Context, id, runID string, msgs []message.Message,
) error {
	return s.appendTurn(ctx, id, runID, TurnOrigin{}, msgs)
}

// AppendTurnWithOriginAndRunID persists one turn the app itself wrote
// (origin.Kind names the author, origin.Payload is its structured
// record) under a run id. The origin is what every reader of the
// archived turn goes by: it is the difference between the user
// speaking and the app reporting something to the model.
func (s *Store) AppendTurnWithOriginAndRunID(
	ctx context.Context,
	id, runID string,
	origin TurnOrigin,
	msgs []message.Message,
) error {
	return s.appendTurn(ctx, id, runID, origin, msgs)
}

// TurnOrigin is the author and structured record of an app-authored
// turn. An empty Kind means the turn is an ordinary one: a user message
// or a model reply.
type TurnOrigin struct {
	// Kind names the author (sessions carries it opaquely; the
	// capability that writes the turn defines the vocabulary).
	Kind string
	// Payload is the author's own JSON record, stored verbatim.
	Payload []byte
}

func (s *Store) appendTurn(
	ctx context.Context, id, runID string, origin TurnOrigin,
	msgs []message.Message,
) error {
	if err := requireID(id); err != nil {
		return err
	}
	archived := filterArchive(msgs)
	if len(archived) == 0 {
		return nil
	}
	conv, convErr := s.db.Conversation(ctx, id)
	c := state.Conversation{ID: id}
	now := time.Now().UTC()
	if convErr == nil {
		c.CreatedAt = conv.CreatedAt
		c.UsageJSON = conv.UsageJSON
		c.ImportSource = conv.ImportSource
		c.ImportReady = conv.ImportReady
		c.Title = conv.Title
	} else if convErr != state.ErrNotFound {
		return convErr
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	// An app-authored turn never names the conversation: the title
	// falls back to the user's first line, and a note the app wrote to
	// the model is not something the user said. Skipping also leaves an
	// empty title empty, so the first real turn still names the chat.
	if origin.Kind == "" && (c.Title == "" || c.TurnCount == 0) {
		if title := firstArchiveTitle(archived); title != "" {
			c.Title = title
		}
	}
	turn, archiveMsgs := s.archiveTurn(id, runID, origin, now, archived)
	return s.db.CommitConversationTurn(ctx, c, turn, archiveMsgs)
}

func (s *Store) archiveTurn(
	id, runID string, origin TurnOrigin, now time.Time,
	archived []message.Message,
) (state.ArchiveTurn, []state.ArchiveMessage) {
	timing := TurnTiming{RequestedAt: now, StartedAt: now}
	if runID != "" {
		if recorded, ok := s.takeTurnTiming(id, runID); ok {
			timing = recorded
		}
	}
	turn := state.ArchiveTurn{
		RunID:       runID,
		At:          now,
		RequestedAt: timing.RequestedAt,
		StartedAt:   timing.StartedAt,
		FinishedAt:  timing.FinishedAt,
		Kind:        origin.Kind,
		PayloadJSON: origin.Payload,
	}
	if turn.RequestedAt.IsZero() {
		turn.RequestedAt = now
	}
	if turn.StartedAt.IsZero() {
		turn.StartedAt = now
	}
	if turn.FinishedAt.IsZero() {
		turn.FinishedAt = now
	}
	artifacts := s.takeArtifacts(id)
	if len(artifacts) > 0 {
		if raw, err := json.Marshal(artifacts); err == nil {
			turn.ArtifactsJSON = raw
		}
	}
	msgs := make([]state.ArchiveMessage, 0, len(archived))
	for _, m := range archived {
		msgs = append(msgs, state.ArchiveMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return turn, msgs
}

// RecordTurnTiming stores timestamps for a run that has not been
// archived yet.
func (s *Store) RecordTurnTiming(
	id, runID string, requestedAt, startedAt time.Time,
) error {
	if err := requireID(id); err != nil {
		return err
	}
	if runID == "" {
		return errdefs.Validationf("sessions: timing run id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnTiming[id] == nil {
		s.turnTiming[id] = make(map[string]TurnTiming)
	}
	s.turnTiming[id][runID] = TurnTiming{
		RequestedAt: requestedAt.UTC(),
		StartedAt:   startedAt.UTC(),
	}
	return nil
}

// RecordTurnEnd records when a run finished plus its terminal status,
// error, and optional provider request/response identifiers.
// Persisting status here keeps the archive in sync with the same
// turn_end event the UI receives.
func (s *Store) RecordTurnEnd(
	id, runID string, finishedAt time.Time,
	status, errText, interruptCause, errorKind, requestID, responseID string,
) error {
	return s.recordTurnEnd(
		id, runID, finishedAt, status, errText,
		interruptCause, errorKind, requestID, responseID)
}

func (s *Store) recordTurnEnd(
	id, runID string, finishedAt time.Time,
	status, errText, interruptCause, errorKind, requestID, responseID string,
) error {
	if err := requireID(id); err != nil {
		return err
	}
	if runID == "" {
		return errdefs.Validationf("sessions: timing run id is required")
	}
	finishedAt = finishedAt.UTC()
	if err := s.db.UpdateArchiveTurnEnd(
		context.Background(), id, runID,
		finishedAt, status, errText,
		interruptCause, errorKind, requestID, responseID,
	); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnTiming[id] == nil {
		s.turnTiming[id] = make(map[string]TurnTiming)
	}
	timing := s.turnTiming[id][runID]
	timing.FinishedAt = finishedAt
	s.turnTiming[id][runID] = timing
	return nil
}

func (s *Store) takeTurnTiming(id, runID string) (TurnTiming, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	timing, ok := s.turnTiming[id][runID]
	if ok {
		delete(s.turnTiming[id], runID)
	}
	return timing, ok
}

// BufferArtifact buffers one observed artifact until the next turn.
func (s *Store) BufferArtifact(id, path string, bytes int) error {
	if err := requireID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.artifactBuf[id]
	for i := range list {
		if list[i].Path == path {
			list[i].Bytes = bytes
			return nil
		}
	}
	s.artifactBuf[id] = append(list, Artifact{Path: path, Bytes: bytes})
	return nil
}

func (s *Store) takeArtifacts(id string) []Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.artifactBuf[id]
	delete(s.artifactBuf, id)
	return list
}

// SaveAttachment copies one user attachment into the session dir.
func (s *Store) SaveAttachment(id, kind, srcPath string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	if kind != "media" && kind != "files" {
		return "", errdefs.Validationf("sessions: unknown attachment kind %q", kind)
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errdefs.Validationf("sessions: attachment is not a regular file")
	}
	// The media kind is inlined into the model prompt (base64), so it
	// shares the prepare hook's inline cap. The files kind is only
	// referenced/copied on disk (forks, future generic attachments)
	// and stays unlimited: a huge PDF or archive must never block a
	// session just because it cannot be embedded in a prompt.
	if kind == "media" && info.Size() > imageutil.MaxInlineImageBytes {
		return "", errdefs.Validationf(
			"sessions: attachment too large (%d bytes)", info.Size())
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"sessions: close attachment source failed", src.Close())
	}()
	dir := filepath.Join(s.dir(id), kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		telemetry.WarnErr(context.Background(),
			"sessions: generate attachment suffix failed", err)
	}
	name := fmt.Sprintf("%d-%x%s", time.Now().UnixNano(), suffix[:], filepath.Ext(srcPath))
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, src); err != nil {
		telemetry.WarnErr(context.Background(),
			"sessions: close partial attachment failed", out.Close())
		telemetry.WarnErr(context.Background(),
			"sessions: remove partial attachment failed", os.Remove(dst))
		return "", err
	}
	if err := out.Close(); err != nil {
		telemetry.WarnErr(context.Background(),
			"sessions: remove attachment after close failure", os.Remove(dst))
		return "", err
	}
	return dst, nil
}

// SaveAttachmentBytes persists one in-memory attachment, used for
// normalized image bytes (for example JPEG q90 output). The same kind
// policy and per-kind limits apply as SaveAttachment.
func (s *Store) SaveAttachmentBytes(
	id, kind, name string, data []byte,
) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	if kind != "media" && kind != "files" {
		return "", errdefs.Validationf("sessions: unknown attachment kind %q", kind)
	}
	if strings.ContainsAny(name, "/\\") {
		return "", errdefs.Validationf(
			"sessions: invalid attachment name %q", name)
	}
	base := filepath.Base(filepath.FromSlash(name))
	if base == "" || base == "." || base == ".." {
		return "", errdefs.Validationf(
			"sessions: invalid attachment name %q", name)
	}
	if kind == "media" && int64(len(data)) > imageutil.MaxInlineImageBytes {
		return "", errdefs.Validationf(
			"sessions: attachment too large (%d bytes)", len(data))
	}
	dir := filepath.Join(s.dir(id), kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	name = fmt.Sprintf("%d-%x%s", time.Now().UnixNano(), suffix[:], filepath.Ext(base))
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := out.Write(data); err != nil {
		closeErr := out.Close()
		telemetry.WarnErr(context.Background(),
			"sessions: close partial attachment failed", closeErr)
		telemetry.WarnErr(context.Background(),
			"sessions: remove partial attachment failed", os.Remove(dst))
		return "", err
	}
	if err := out.Close(); err != nil {
		telemetry.WarnErr(context.Background(),
			"sessions: remove attachment after close failure", os.Remove(dst))
		return "", err
	}
	return dst, nil
}

// History returns the most recent n archived messages, oldest first.
func (s *Store) History(ctx context.Context, id string, n int) ([]message.Message, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	if n == 0 {
		n = s.window
	}
	msgs, err := s.db.ListArchiveMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, message.Message{
			Role:    message.Role(m.Role),
			Content: m.Content,
		})
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

// SearchOptions, SearchHit and SearchResult are the message-search
// shapes; the SQLite layer owns them and this package re-exports them
// so callers (the session_search tool) depend on the store, not on
// state.
type (
	SearchOptions = state.SearchOptions
	SearchHit     = state.SearchHit
	SearchResult  = state.SearchResult
)

// SearchMessages runs a full-text search over the workspace's archived
// messages: every conversation the workspace holds, current and older.
// The store writes the index in the same transaction as the archive,
// so a message is searchable as soon as its turn is committed.
func (s *Store) SearchMessages(
	ctx context.Context, query string, opts SearchOptions,
) (SearchResult, error) {
	return s.db.SearchMessages(ctx, query, opts)
}

// Turns returns every archived turn, oldest first.
func (s *Store) Turns(ctx context.Context, id string) ([]TurnRecord, error) {
	return s.TurnsPage(ctx, id, 0, 0)
}

// TurnsPage returns archived turns oldest first. limit <= 0 returns
// every turn; beforeSeq > 0 keeps only turns older than that seq, so the
// UI can hydrate the newest turns first and page backwards on demand
// instead of loading a whole long session at startup.
func (s *Store) TurnsPage(
	ctx context.Context, id string, limit int, beforeSeq int64,
) ([]TurnRecord, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	turns, err := s.db.ListArchiveTurnsPage(ctx, id, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	return s.turnRecords(ctx, id, turns)
}

// TurnsSince returns archived turns newer than seq, oldest first. The
// transcript holds the newest seq it has and asks for what was appended
// after it: a delegation note is written when its subagent finishes,
// which can be long after the turn that spawned it ended, so no live
// stream ever carried it and a full re-read is not worth the payload.
func (s *Store) TurnsSince(
	ctx context.Context, id string, afterSeq int64, limit int,
) ([]TurnRecord, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	turns, err := s.db.ListArchiveTurnsAfter(ctx, id, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return s.turnRecords(ctx, id, turns)
}

// turnRecords lowers archived turn rows and their messages into the
// shared TurnRecord shape: one message query for the whole page, then
// grouped back per turn.
func (s *Store) turnRecords(
	ctx context.Context, id string, turns []state.ArchiveTurn,
) ([]TurnRecord, error) {
	if len(turns) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(turns))
	for _, t := range turns {
		ids = append(ids, t.ID)
	}
	msgs, err := s.db.ListArchiveMessagesForTurns(ctx, id, ids)
	if err != nil {
		return nil, err
	}
	byTurn := make(map[int64][]state.ArchiveMessage)
	for _, m := range msgs {
		byTurn[m.TurnID] = append(byTurn[m.TurnID], m)
	}
	out := make([]TurnRecord, 0, len(turns))
	for _, t := range turns {
		out = append(out, archiveTurnRecord(ctx, id, t, byTurn[t.ID]))
	}
	return out, nil
}

// TurnByRunID returns one archived turn for a completed run, without
// loading the rest of the conversation. It is the reconciliation
// endpoint for live turn_end events whose streamed deltas may have
// been coalesced or dropped.
func (s *Store) TurnByRunID(
	ctx context.Context, conversationID, runID string,
) (TurnRecord, error) {
	if err := requireID(conversationID); err != nil {
		return TurnRecord{}, err
	}
	if runID == "" {
		return TurnRecord{},
			errdefs.Validationf("session store: run id is required")
	}
	turn, msgs, err := s.db.ArchiveTurnByRun(ctx, conversationID, runID)
	if err != nil {
		return TurnRecord{}, fmt.Errorf(
			"sessions: turn by run %q: %w", runID, err)
	}
	return archiveTurnRecord(ctx, conversationID, turn, msgs), nil
}

// archiveTurnRecord lowers one SQLite turn row into the shared
// TurnRecord shape used by the UI binding.
func archiveTurnRecord(
	ctx context.Context,
	conversationID string,
	turn state.ArchiveTurn,
	msgs []state.ArchiveMessage,
) TurnRecord {
	rec := TurnRecord{
		Seq:            turn.Seq,
		At:             turn.At,
		RequestedAt:    turn.RequestedAt,
		StartedAt:      turn.StartedAt,
		FinishedAt:     turn.FinishedAt,
		RunID:          turn.RunID,
		Status:         turn.Status,
		Error:          turn.Error,
		InterruptCause: turn.InterruptCause,
		ErrorKind:      turn.ErrorKind,
		RequestID:      turn.RequestID,
		ResponseID:     turn.ResponseID,
		Kind:           turn.Kind,
	}
	if payload := strings.TrimSpace(string(turn.PayloadJSON)); payload != "" {
		rec.Payload = json.RawMessage(turn.PayloadJSON)
	}
	for _, m := range msgs {
		rec.Messages = append(rec.Messages, message.Message{
			Role:    message.Role(m.Role),
			Content: m.Content,
		})
	}
	if len(turn.ArtifactsJSON) > 0 {
		if err := json.Unmarshal(turn.ArtifactsJSON, &rec.Artifacts); err != nil {
			telemetry.WarnErr(ctx,
				"sessions: decode turn artifacts failed", err,
				otellog.String("conversation.id", conversationID))
		}
	}
	return rec
}

// RolloutPath returns the JSONL audit path for one conversation.
func (s *Store) RolloutPath(id string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.dir(id), "rollout.jsonl"), nil
}

// List returns metadata for every conversation, newest first.
func (s *Store) List() ([]Meta, error) {
	convs, err := s.db.ListConversations(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]Meta, 0, len(convs))
	for _, c := range convs {
		if c.ImportSource != "" && !c.ImportReady {
			continue
		}
		var usage Usage
		if len(c.UsageJSON) > 0 {
			if err := json.Unmarshal(c.UsageJSON, &usage); err != nil {
				telemetry.WarnErr(context.Background(),
					"sessions: decode conversation usage failed", err,
					otellog.String("conversation.id", c.ID))
			}
		}
		usage.Model = NormalizeModelName(usage.Model)
		// A conversation whose first turn started but has not archived
		// yet carries only a title and zero turns. It must stay visible
		// while the run is in flight (and after a crash), so skip only
		// zero-turn rows with no title at all.
		if c.TurnCount == 0 && usage == (Usage{}) &&
			strings.TrimSpace(c.Title) == "" {
			continue
		}
		m := Meta{
			ID:        c.ID,
			Title:     c.Title,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
			Turns:     c.TurnCount,
			Messages:  c.MessageCount,
			Usage:     usage,
		}
		if m.Messages == 0 {
			m.Messages = c.TurnCount
		}
		if m.Title == "" {
			m.Title = "(empty)"
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// Title returns the conversation's fallback title, as written by the
// archive (see state.Conversation). A user- or model-chosen title lives
// in the conversation_state document "title" and is overlaid by the
// readers that display one.
func (s *Store) Title(id string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	c, err := s.db.Conversation(context.Background(), id)
	if err == state.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return c.Title, nil
}

// FirstUserMessage returns the first non-empty user message text. Turns
// the app itself wrote are skipped: a delegation note is the app
// reporting to the model, and a title (or anything else asking what the
// user said) must not read it as speech.
func (s *Store) FirstUserMessage(id string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	msgs, err := s.db.ListArchiveMessagesWithoutTurnKind(
		context.Background(), id,
	)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		if m.Role != string(message.RoleUser) {
			continue
		}
		if text := strings.TrimSpace(m.Content.Text()); text != "" {
			return text, nil
		}
	}
	return "", nil
}

// SeedStartTitle records the provisional archive fallback title on the
// conversation row as soon as the session's first turn starts. The row
// intentionally has zero archived turns until the turn commits, so List
// must keep it visible. The first archive replaces this provisional
// title with the archive's own first-user-message fallback (subsequent
// archives preserve it), and the LLM auto-title stays free to overlay
// conversation_state["title"] afterwards.
func (s *Store) SeedStartTitle(
	ctx context.Context, id string, msgs []message.Message,
) error {
	if err := requireID(id); err != nil {
		return err
	}
	archived := filterArchive(msgs)
	title := firstArchiveTitle(archived)
	if title == "" {
		return nil
	}
	return s.db.EnsureConversation(ctx, state.Conversation{
		ID:    id,
		Title: title,
	})
}

// LoadUsage returns the cumulative token usage for a session.
func (s *Store) LoadUsage(ctx context.Context, id string) (Usage, error) {
	if err := requireID(id); err != nil {
		return Usage{}, err
	}
	c, err := s.db.Conversation(ctx, id)
	if err == state.ErrNotFound {
		return Usage{}, nil
	}
	if err != nil {
		return Usage{}, err
	}
	var usage Usage
	if len(c.UsageJSON) > 0 {
		if err := json.Unmarshal(c.UsageJSON, &usage); err != nil {
			telemetry.WarnErr(ctx, "sessions: decode usage failed", err,
				otellog.String("conversation.id", id))
		}
	}
	usage.Model = NormalizeModelName(usage.Model)
	return usage, nil
}

// RecordUsage persists the cumulative token usage for a session. The
// number lives on the conversations row as a cache beside the archive
// (state.Conversation documents the ownership): it answers "how much
// did this chat cost" for the UI without summing the transcript.
func (s *Store) RecordUsage(ctx context.Context, id string, usage Usage) error {
	if err := requireID(id); err != nil {
		return err
	}
	usage.Model = NormalizeModelName(usage.Model)
	c, err := s.db.Conversation(ctx, id)
	if err == state.ErrNotFound {
		c = state.Conversation{ID: id, CreatedAt: time.Now().UTC()}
		if err := s.db.EnsureConversation(ctx, c); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	c.UsageJSON = raw
	c.UpdatedAt = time.Now().UTC()
	return s.db.UpsertConversation(ctx, c)
}

// RecordUsageIfEmpty persists cumulative usage for a session only when
// it has no recorded totals yet, returning whether the write happened.
// The read-if-empty-and-set check runs under the store mutex, so
// concurrent imports of the same source cannot double count imported
// token totals.
func (s *Store) RecordUsageIfEmpty(
	ctx context.Context,
	id string,
	usage Usage,
) (bool, error) {
	if err := requireID(id); err != nil {
		return false, err
	}
	if usage.TotalTokens <= 0 {
		return false, nil
	}
	usage.Model = NormalizeModelName(usage.Model)
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.db.Conversation(ctx, id)
	if err == state.ErrNotFound {
		c = state.Conversation{ID: id, CreatedAt: time.Now().UTC()}
		if err := s.db.EnsureConversation(ctx, c); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	if len(c.UsageJSON) > 0 {
		var existing Usage
		if err := json.Unmarshal(c.UsageJSON, &existing); err != nil {
			telemetry.WarnErr(ctx, "sessions: decode usage before empty-seed failed", err,
				otellog.String("conversation.id", id))
		} else if existing.TotalTokens > 0 {
			return false, nil
		}
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return false, err
	}
	c.UsageJSON = raw
	c.UpdatedAt = time.Now().UTC()
	return true, s.db.UpsertConversation(ctx, c)
}

// AddUsage accumulates one usage delta onto the cumulative usage
// already recorded for a session. Turn-end persistence and background
// generations such as auto titles both feed deltas here, so a session's
// total_tokens stays cumulative instead of reflecting only the last
// call. It serializes on the store mutex to keep concurrent turn ends
// from overwriting each other.
func (s *Store) AddUsage(ctx context.Context, id string, delta Usage) error {
	if err := requireID(id); err != nil {
		return err
	}
	if delta.TotalTokens <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.db.Conversation(ctx, id)
	if err == state.ErrNotFound {
		c = state.Conversation{ID: id, CreatedAt: time.Now().UTC()}
		if err := s.db.EnsureConversation(ctx, c); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var usage Usage
	if len(c.UsageJSON) > 0 {
		if err := json.Unmarshal(c.UsageJSON, &usage); err != nil {
			telemetry.WarnErr(ctx, "sessions: decode usage before add failed", err,
				otellog.String("conversation.id", id))
		}
	}
	usage.Model = NormalizeModelName(usage.Model)
	delta.Model = NormalizeModelName(delta.Model)
	if delta.Model != "" {
		usage.Model = delta.Model
	}
	usage.InputTokens += delta.InputTokens
	usage.OutputTokens += delta.OutputTokens
	usage.TotalTokens += delta.TotalTokens
	usage.CacheReadTokens += delta.CacheReadTokens
	usage.CacheWriteTokens += delta.CacheWriteTokens
	usage.ReasoningTokens += delta.ReasoningTokens
	usage.LatencyMs += delta.LatencyMs
	usage.Calls += delta.Calls

	raw, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	c.UsageJSON = raw
	c.UpdatedAt = time.Now().UTC()
	return s.db.UpsertConversation(ctx, c)
}

// Remove removes one conversation and its on-disk directory.
func (s *Store) Remove(ctx context.Context, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(ctx, id)
}

func (s *Store) removeLocked(ctx context.Context, id string) error {
	delete(s.turnTiming, id)
	delete(s.artifactBuf, id)
	if err := s.db.DeleteConversationRows(ctx, id); err != nil {
		return fmt.Errorf("sessions: remove %s state: %w", id, err)
	}
	if err := os.RemoveAll(s.dir(id)); err != nil {
		return fmt.Errorf("sessions: remove %s dir: %w", id, err)
	}
	return nil
}

func (s *Store) dir(id string) string {
	return filepath.Join(s.root, id)
}

// filterArchive keeps every message that carries content. Archive rows
// persist canonical message.Content JSON, so part kinds are preserved
// verbatim instead of being whitelisted: dropping unknown parts would
// lose data, and erroring here would fail an otherwise completed turn.
func filterArchive(msgs []message.Message) []message.Message {
	var archived []message.Message
	for _, m := range msgs {
		if len(m.Content.Parts) > 0 {
			archived = append(archived, message.Message{
				Role:    m.Role,
				Content: m.Content.Clone(),
			})
		}
	}
	return archived
}

func requireID(id string) error {
	if !ValidID(id) {
		return errdefs.Validationf("sessions: invalid session id %q", id)
	}
	return nil
}

func firstArchiveTitle(msgs []message.Message) string {
	for _, m := range msgs {
		if m.Role != message.RoleUser {
			continue
		}
		if title := firstLine(m.Content.Text()); title != "" {
			return title
		}
		for _, part := range m.Content.Parts {
			if _, ok := part.(message.TextPart); !ok {
				return "[attachment]"
			}
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const maxTitle = 70
	runes := []rune(s)
	if len(runes) > maxTitle {
		return string(runes[:maxTitle]) + "…"
	}
	return s
}

// ---------- deploy resource ----------

// Factory builds the session store resource. StoreFor lets a host
// share one Store across runtimes; nil opens a private store.
type Factory struct {
	StoreFor func(
		ctx context.Context, root string, window int,
	) (*Store, error)
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "opencraft"}
}

type settings struct {
	Root   string `json:"root"`
	Window int    `json:"window,omitempty"`
}

// New implements resource.Factory.
func (f Factory) New(ctx context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[settings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf("session store: %v", err)
	}
	if f.StoreFor == nil {
		return nil, errdefs.NotAvailablef(
			"session store: StoreFor is required; schema migration is " +
				"centralized in internal/foundation/compat")
	}
	return f.StoreFor(ctx, s.Root, s.Window)
}
