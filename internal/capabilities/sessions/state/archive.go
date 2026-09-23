package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
)

// Conversation is the SQLite-backed session index row. It replaces the
// legacy per-session meta.json and is the source of truth for the
// resume list.
//
// Column ownership (docs/session-data-model.md §1 has the full model):
// this row is an index over the transcript, never a second copy of it.
//
//   - ID / CreatedAt / UpdatedAt: identity and bookkeeping.
//   - Title: the fallback title, derived from the first user message of
//     the first archived turn (Store.appendTurn writes it, then the
//     CASE in CommitConversationTurn keeps it stable; Store.
//     SeedStartTitle seeds it before that turn commits). A title a
//     person or the auto-titler chose lives in the conversation_state
//     document "title" and overlays this column wherever the UI reads
//     it (bindings.listStoredMetas, host/title.go); the column keeps
//     the fallback, so losing the document only loses the rename.
//   - TurnCount / MessageCount: caches of what archive_turns and
//     archive_messages hold, bumped in the same transaction that
//     appends a turn. They serve the session list; the archive is the
//     fact, and recomputing them from the archive must give these
//     numbers.
//   - UsageJSON: the cumulative usage cache, maintained by
//     sessions.Store.RecordUsage/AddUsage. Same rule as the counters.
//   - ImportSource / ImportReady: import bookkeeping — which legacy
//     session this came from, and whether its import finished. Written
//     by the import path only.
type Conversation struct {
	ID           string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	TurnCount    int
	MessageCount int
	UsageJSON    []byte
	ImportSource string
	ImportReady  bool
}

// ArchiveTurn is one archived execution turn stored in SQLite.
type ArchiveTurn struct {
	ID          int64
	Seq         int
	RunID       string
	At          time.Time
	RequestedAt time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	Status      string
	Error       string
	// InterruptCause / ErrorKind are the structured class of a failed
	// turn (host.TurnErrorClass), stored so a resumed transcript can
	// render the same copy as the live turn without parsing Error.
	InterruptCause string
	ErrorKind      string
	// RequestID is the provider-assigned request identifier of the
	// terminal operation when the provider reported one. Failures
	// usually carry it on the error chain; successful generations
	// carry it on the terminal finish delta. Empty when unavailable.
	RequestID string
	// ResponseID is the provider-assigned identifier of the response
	// object (chat/message id). It is only known once a response
	// started, so it typically populates successful turns and may be
	// the only correlation id available there.
	ResponseID    string
	ArtifactsJSON []byte
	// Kind names the author of a turn the app itself wrote (a
	// delegation note, for example); empty for every turn a user or
	// model produced. PayloadJSON is that author's own structured
	// record, stored verbatim — the archive never inspects it, and a
	// reader that does not know the kind ignores it.
	Kind        string
	PayloadJSON []byte
}

// ArchiveMessage is one full-fidelity message stored in SQLite.
type ArchiveMessage struct {
	ID        int64
	TurnID    int64
	Seq       int
	Role      string
	Content   message.Content
	CreatedAt time.Time
}

// archiveTurnColumns is the column list every archive-turn read shares,
// in the order scanArchiveTurn expects. Keeping it in one place means a
// new turn column is added to the write and every read together.
const archiveTurnColumns = `id, conversation_id, seq, run_id, at,
	requested_at, started_at, finished_at,
	status, error, interrupt_cause, error_kind,
	request_id, response_id, artifacts_json, kind, payload_json`

// scanArchiveTurn reads one row selected with archiveTurnColumns.
func scanArchiveTurn(row rowScanner) (ArchiveTurn, error) {
	var t ArchiveTurn
	var convID string
	var run sql.NullString
	var at, requested, started, finished, status, errText string
	var interruptCause, errorKind, requestID, responseID, artifacts string
	var kind, payload string
	if err := row.Scan(&t.ID, &convID, &t.Seq, &run, &at,
		&requested, &started, &finished, &status, &errText,
		&interruptCause, &errorKind,
		&requestID, &responseID, &artifacts,
		&kind, &payload); err != nil {
		return ArchiveTurn{}, err
	}
	t.RunID = run.String
	t.At = parseTime(at)
	t.RequestedAt = parseTime(requested)
	t.StartedAt = parseTime(started)
	t.FinishedAt = parseTime(finished)
	t.Status = status
	t.Error = errText
	t.InterruptCause = interruptCause
	t.ErrorKind = errorKind
	t.RequestID = requestID
	t.ResponseID = responseID
	t.ArtifactsJSON = []byte(artifacts)
	t.Kind = kind
	t.PayloadJSON = []byte(payload)
	return t, nil
}

// EnsureConversation inserts a conversation row when missing.
func (s *Store) EnsureConversation(ctx context.Context, c Conversation) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("state: conversation id is required")
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if len(c.UsageJSON) == 0 {
		c.UsageJSON = []byte("{}")
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO conversations(
			id, title, created_at, updated_at, turn_count, message_count,
			usage_json, import_source, import_ready
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		c.ID, c.Title,
		c.CreatedAt.UTC().Format(time.RFC3339Nano),
		c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		c.TurnCount, c.MessageCount, string(c.UsageJSON),
		c.ImportSource, boolInt(c.ImportReady),
	)
	if err != nil {
		return fmt.Errorf("state: ensure conversation: %w", err)
	}
	return nil
}

// UpsertConversation overwrites mutable conversation metadata.
func (s *Store) UpsertConversation(ctx context.Context, c Conversation) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("state: conversation id is required")
	}
	if len(c.UsageJSON) == 0 {
		c.UsageJSON = []byte("{}")
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO conversations(
			id, title, created_at, updated_at, turn_count, message_count,
			usage_json, import_source, import_ready
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			updated_at = excluded.updated_at,
			turn_count = excluded.turn_count,
			message_count = excluded.message_count,
			usage_json = excluded.usage_json,
			import_source = excluded.import_source,
			import_ready = excluded.import_ready`,
		c.ID, c.Title,
		c.CreatedAt.UTC().Format(time.RFC3339Nano),
		c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		c.TurnCount, c.MessageCount, string(c.UsageJSON),
		c.ImportSource, boolInt(c.ImportReady),
	)
	if err != nil {
		return fmt.Errorf("state: upsert conversation: %w", err)
	}
	return nil
}

// ListConversations returns every conversation, newest first.
func (s *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	rows, err := s.db.SQLDB().QueryContext(ctx, `
		SELECT id, title, created_at, updated_at, turn_count, message_count,
			usage_json, import_source, import_ready
		FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("state: list conversations: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close conversation rows failed", rows.Close())
	}()
	var out []Conversation
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Conversation returns one conversation row.
func (s *Store) Conversation(ctx context.Context, id string) (Conversation, error) {
	row := s.db.SQLDB().QueryRowContext(ctx, `
		SELECT id, title, created_at, updated_at, turn_count, message_count,
			usage_json, import_source, import_ready
		FROM conversations WHERE id = ?`, id)
	c, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	return c, err
}

// ConversationByImportSource returns the conversation previously
// imported from source.
func (s *Store) ConversationByImportSource(
	ctx context.Context, source string,
) (Conversation, bool, error) {
	row := s.db.SQLDB().QueryRowContext(ctx, `
		SELECT id, title, created_at, updated_at, turn_count, message_count,
			usage_json, import_source, import_ready
		FROM conversations WHERE import_source = ? ORDER BY updated_at DESC LIMIT 1`,
		source)
	c, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, false, nil
	}
	if err != nil {
		return Conversation{}, false, err
	}
	return c, true, nil
}

// maxImportSourceQuery bounds one ImportReadyBySources call. Lists are
// bounded by what a plugin can enumerate from Codex, so a few thousand
// is more than enough; the cap keeps the SQLite IN clause reasonable.
const maxImportSourceQuery = 1000

// ImportReadyBySources returns the newest import-ready conversation id
// for each of the given import sources that exists in this workspace.
// Only conversations whose memory seed completed are reported, which
// matches what the resume list shows.
func (s *Store) ImportReadyBySources(
	ctx context.Context, sources []string,
) (map[string]string, error) {
	out := make(map[string]string)
	if len(sources) == 0 {
		return out, nil
	}
	if len(sources) > maxImportSourceQuery {
		return nil, fmt.Errorf(
			"state: import source query exceeds %d sources",
			maxImportSourceQuery)
	}
	seen := make(map[string]bool, len(sources))
	placeholders := make([]string, 0, len(sources))
	args := make([]any, 0, len(sources))
	for _, raw := range sources {
		src := strings.TrimSpace(raw)
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		placeholders = append(placeholders, "?")
		args = append(args, src)
	}
	if len(placeholders) == 0 {
		return out, nil
	}
	query := `
		SELECT import_source, id FROM conversations
		WHERE import_ready = 1
		  AND import_source IN (` + strings.Join(placeholders, ", ") + `)
		ORDER BY updated_at DESC`
	rows, err := s.db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: query imported sources: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close imported source rows failed", rows.Close())
	}()
	for rows.Next() {
		var source, id string
		if err := rows.Scan(&source, &id); err != nil {
			return nil, fmt.Errorf("state: scan imported source: %w", err)
		}
		if _, ok := out[source]; !ok {
			out[source] = id
		}
	}
	return out, rows.Err()
}

// GetConversationState loads one per-conversation JSON document.
func (s *Store) GetConversationState(
	ctx context.Context, conversationID, name string,
) ([]byte, error) {
	var raw string
	err := s.db.SQLDB().QueryRowContext(ctx, `
		SELECT value_json FROM conversation_state
		WHERE conversation_id = ? AND name = ?`, conversationID, name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("state: read conversation state: %w", err)
	}
	return []byte(raw), nil
}

// SetConversationState writes one per-conversation JSON document.
func (s *Store) SetConversationState(
	ctx context.Context, conversationID, name string, value []byte,
) error {
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("state: conversation state key is required")
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		INSERT INTO conversation_state(
			conversation_id, name, value_json, updated_at
		) VALUES (?, ?, ?, ?)
		ON CONFLICT(conversation_id, name) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at`,
		conversationID, name, string(value),
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("state: write conversation state: %w", err)
	}
	return nil
}

// CommitConversationTurn atomically appends one full-fidelity turn, its
// messages, the search-index rows for those messages, and the
// conversation's counter caches.
//
// It is the one place a transcript turn is written. Nothing else may
// join this transaction: the transcript is the conversation, and the
// only derived state that has to be exact — the full-text index the
// archive must never lag, and the counters a sidebar sorts by — is
// written here so it cannot drift. Readers that need more (a summary
// tree, a model window) derive it from these rows afterwards.
func (s *Store) CommitConversationTurn(
	ctx context.Context,
	c Conversation,
	turn ArchiveTurn,
	msgs []ArchiveMessage,
) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("state: conversation id is required")
	}
	if err := s.EnsureConversation(ctx, c); err != nil {
		return err
	}
	tx, err := s.db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin conversation turn: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "state: rollback commit turn failed", err)
		}
	}()

	if turn.RunID != "" {
		var existing int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM archive_turns
			WHERE conversation_id = ? AND run_id = ?`,
			c.ID, turn.RunID).Scan(&existing)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("state: lookup turn by run: %w", err)
		}
	}

	var turnSeq, messageSeq int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) + 1 FROM archive_turns
		WHERE conversation_id = ?`, c.ID).Scan(&turnSeq); err != nil {
		return fmt.Errorf("state: next turn seq: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), -1) + 1 FROM archive_messages
		WHERE conversation_id = ?`, c.ID).Scan(&messageSeq); err != nil {
		return fmt.Errorf("state: next message seq: %w", err)
	}

	now := time.Now().UTC()
	if turn.At.IsZero() {
		turn.At = now
	}
	if turn.RequestedAt.IsZero() {
		turn.RequestedAt = turn.At
	}
	if turn.StartedAt.IsZero() {
		turn.StartedAt = turn.At
	}
	if turn.FinishedAt.IsZero() {
		turn.FinishedAt = turn.At
	}
	if len(turn.ArtifactsJSON) == 0 {
		turn.ArtifactsJSON = []byte("[]")
	}
	var runID any
	if turn.RunID != "" {
		runID = turn.RunID
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO archive_turns(
			conversation_id, seq, run_id, at,
			requested_at, started_at, finished_at,
			status, error, interrupt_cause, error_kind,
			request_id, response_id, artifacts_json,
			kind, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, turnSeq, runID,
		turn.At.UTC().Format(time.RFC3339Nano),
		turn.RequestedAt.UTC().Format(time.RFC3339Nano),
		turn.StartedAt.UTC().Format(time.RFC3339Nano),
		turn.FinishedAt.UTC().Format(time.RFC3339Nano),
		turn.Status,
		turn.Error,
		turn.InterruptCause,
		turn.ErrorKind,
		turn.RequestID,
		turn.ResponseID,
		string(turn.ArtifactsJSON),
		turn.Kind,
		string(turn.PayloadJSON),
	)
	if err != nil {
		return fmt.Errorf("state: insert archive turn: %w", err)
	}
	turnID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("state: archive turn id: %w", err)
	}

	for i := range msgs {
		msgs[i].TurnID = turnID
		msgs[i].Seq = int(messageSeq) + i
		if msgs[i].CreatedAt.IsZero() {
			msgs[i].CreatedAt = now
		}
		content, err := json.Marshal(msgs[i].Content)
		if err != nil {
			return fmt.Errorf("state: marshal archive message: %w", err)
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO archive_messages(
				conversation_id, turn_id, seq, role, content_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, turnID, msgs[i].Seq, msgs[i].Role,
			string(content),
			msgs[i].CreatedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return fmt.Errorf("state: insert archive message: %w", err)
		}
		// The message's search index row is written here, inside the
		// same transaction: the index can never lag the archive, and a
		// rolled-back turn leaves no orphan hit behind.
		messageID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("state: archive message id: %w", err)
		}
		if err := insertMessageFTS(ctx, tx, c.ID, msgs[i].Role,
			msgs[i].CreatedAt.UTC().Format(time.RFC3339Nano), messageID,
			indexMessageText(msgs[i].Content),
		); err != nil {
			return err
		}
	}

	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE conversations SET
			title = CASE
				WHEN title = '' OR turn_count = 0 THEN ?
				ELSE title
			END,
			updated_at = ?,
			turn_count = turn_count + 1,
			message_count = message_count + ?
		WHERE id = ?`,
		c.Title, c.UpdatedAt.UTC().Format(time.RFC3339Nano),
		len(msgs), c.ID,
	); err != nil {
		return fmt.Errorf("state: update conversation counts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit conversation turn: %w", err)
	}
	return nil
}

// ListArchiveTurns returns every turn of one conversation.
func (s *Store) ListArchiveTurns(
	ctx context.Context, conversationID string,
) ([]ArchiveTurn, error) {
	return s.ListArchiveTurnsPage(ctx, conversationID, 0, 0)
}

// ListArchiveTurnsPage returns up to limit turns of one conversation,
// oldest first. limit <= 0 returns every turn; beforeSeq > 0 keeps only
// turns with a seq below it, which is how the UI pages backwards through
// a long session instead of loading the whole archive at startup.
func (s *Store) ListArchiveTurnsPage(
	ctx context.Context, conversationID string, limit int, beforeSeq int64,
) ([]ArchiveTurn, error) {
	query := `
		SELECT ` + archiveTurnColumns + `
		FROM archive_turns WHERE conversation_id = ?`
	args := []any{conversationID}
	if beforeSeq > 0 {
		query += ` AND seq < ?`
		args = append(args, beforeSeq)
	}
	if limit > 0 {
		// Newest first so LIMIT keeps the turns closest to the cursor,
		// then reversed below to the oldest-first order callers expect.
		query += ` ORDER BY seq DESC LIMIT ?`
		args = append(args, limit)
	} else {
		query += ` ORDER BY seq`
	}
	out, err := s.scanArchiveTurns(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if limit > 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

// ListArchiveTurnsAfter returns up to limit turns newer than seq, oldest
// first. It is the tail cursor to ListArchiveTurnsPage's backwards one:
// a reader holding the newest turn it knows asks what was appended
// since, instead of re-reading the turns it already has.
func (s *Store) ListArchiveTurnsAfter(
	ctx context.Context, conversationID string, afterSeq int64, limit int,
) ([]ArchiveTurn, error) {
	query := `
		SELECT ` + archiveTurnColumns + `
		FROM archive_turns WHERE conversation_id = ? AND seq > ?`
	args := []any{conversationID, afterSeq}
	if limit > 0 {
		query += ` ORDER BY seq LIMIT ?`
		args = append(args, limit)
	} else {
		query += ` ORDER BY seq`
	}
	return s.scanArchiveTurns(ctx, query, args...)
}

// scanArchiveTurns runs one archive-turn query and reads every row.
func (s *Store) scanArchiveTurns(
	ctx context.Context, query string, args ...any,
) ([]ArchiveTurn, error) {
	rows, err := s.db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list archive turns: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close archive turn rows failed", rows.Close())
	}()
	var out []ArchiveTurn
	for rows.Next() {
		t, err := scanArchiveTurn(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan archive turn: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListArchiveMessages returns every archived message in conversation
// order.
func (s *Store) ListArchiveMessages(
	ctx context.Context, conversationID string,
) ([]ArchiveMessage, error) {
	rows, err := s.db.SQLDB().QueryContext(ctx, `
		SELECT id, turn_id, seq, role, content_json, created_at
		FROM archive_messages WHERE conversation_id = ?
		ORDER BY seq`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("state: list archive messages: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close archive message rows failed", rows.Close())
	}()
	var out []ArchiveMessage
	for rows.Next() {
		m, err := scanArchiveMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan archive message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanArchiveMessage reads one archive_messages row selected as id,
// turn_id, seq, role, content_json, created_at.
func scanArchiveMessage(row rowScanner) (ArchiveMessage, error) {
	var m ArchiveMessage
	var content, createdAt string
	if err := row.Scan(&m.ID, &m.TurnID, &m.Seq, &m.Role,
		&content, &createdAt); err != nil {
		return ArchiveMessage{}, err
	}
	if err := json.Unmarshal([]byte(content), &m.Content); err != nil {
		return ArchiveMessage{}, fmt.Errorf("decode archive message: %w", err)
	}
	m.CreatedAt = parseTime(createdAt)
	return m, nil
}

// ListArchiveMessagesWithoutTurnKind returns the conversation's archived
// messages in conversation order, skipping every message whose turn
// carries a kind. Such a turn is app-authored — a delegation note, for
// example — so it speaks to the model rather than as someone in the
// conversation, and a reader asking what the user said (title
// derivation, above all) must not hear it. The archive keeps every row;
// this is a reading rule, not a rewrite.
func (s *Store) ListArchiveMessagesWithoutTurnKind(
	ctx context.Context, conversationID string,
) ([]ArchiveMessage, error) {
	rows, err := s.db.SQLDB().QueryContext(ctx, `
		SELECT m.id, m.turn_id, m.seq, m.role, m.content_json, m.created_at
		FROM archive_messages m
		JOIN archive_turns t ON t.id = m.turn_id
		WHERE m.conversation_id = ? AND t.kind = ''
		ORDER BY m.seq`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("state: list unkinded archive messages: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx,
			"state: close unkinded archive message rows failed", rows.Close())
	}()
	var out []ArchiveMessage
	for rows.Next() {
		m, err := scanArchiveMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan unkinded archive message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListArchiveMessagesForTurns loads the messages of the given turns, in
// conversation order. Paged reads use it so a page costs one query over
// the page's messages instead of reading every message in the session.
func (s *Store) ListArchiveMessagesForTurns(
	ctx context.Context, conversationID string, turnIDs []int64,
) ([]ArchiveMessage, error) {
	if len(turnIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]byte, 0, len(turnIDs)*2)
	for i := range turnIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}
	query := `
		SELECT id, turn_id, seq, role, content_json, created_at
		FROM archive_messages
		WHERE conversation_id = ? AND turn_id IN (` +
		string(placeholders) + `)
		ORDER BY seq`
	args := make([]any, 0, len(turnIDs)+1)
	args = append(args, conversationID)
	for _, id := range turnIDs {
		args = append(args, id)
	}
	rows, err := s.db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list archive messages for turns: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close archive turn message rows failed", rows.Close())
	}()
	var out []ArchiveMessage
	for rows.Next() {
		m, err := scanArchiveMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan archive turn message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ArchiveTurnByRun returns one archived turn and its messages for a
// completed run. The run id is unique per conversation, so callers can
// reconcile a single live turn without loading the whole session.
func (s *Store) ArchiveTurnByRun(
	ctx context.Context, conversationID, runID string,
) (ArchiveTurn, []ArchiveMessage, error) {
	if conversationID == "" || runID == "" {
		return ArchiveTurn{}, nil,
			fmt.Errorf("state: conversation/run ids are required")
	}
	t, err := scanArchiveTurn(s.db.SQLDB().QueryRowContext(ctx, `
		SELECT `+archiveTurnColumns+`
		FROM archive_turns
		WHERE conversation_id = ? AND run_id = ?`,
		conversationID, runID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArchiveTurn{}, nil, ErrNotFound
		}
		return ArchiveTurn{}, nil,
			fmt.Errorf("state: get archive turn by run: %w", err)
	}

	rows, err := s.db.SQLDB().QueryContext(ctx, `
		SELECT id, turn_id, seq, role, content_json, created_at
		FROM archive_messages WHERE turn_id = ? ORDER BY seq`, t.ID)
	if err != nil {
		return ArchiveTurn{}, nil,
			fmt.Errorf("state: list archive turn messages: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx,
			"state: close archive turn message rows failed", rows.Close())
	}()
	var msgs []ArchiveMessage
	for rows.Next() {
		m, err := scanArchiveMessage(rows)
		if err != nil {
			return ArchiveTurn{}, nil,
				fmt.Errorf("state: scan archive turn message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return t, msgs, rows.Err()
}

// UpdateArchiveTurnEnd records the terminal status/error and finish
// time of one run. The status/error fields are written by the host
// after the engine observer/committer has already inserted the turn,
// so archive rows always carry the same status the UI event reports.
func (s *Store) UpdateArchiveTurnEnd(
	ctx context.Context, conversationID, runID string,
	finishedAt time.Time,
	status, errText, interruptCause, errorKind, requestID, responseID string,
) error {
	if runID == "" {
		return fmt.Errorf("state: run id is required")
	}
	finishedAt = finishedAt.UTC()
	_, err := s.db.SQLDB().ExecContext(ctx, `
		UPDATE archive_turns SET
			finished_at = ?,
			status = ?,
			error = ?,
			interrupt_cause = ?,
			error_kind = ?,
			request_id = ?,
			response_id = ?
		WHERE conversation_id = ? AND run_id = ?`,
		finishedAt.Format(time.RFC3339Nano),
		status, errText, interruptCause, errorKind,
		requestID, responseID, conversationID, runID)
	if err != nil {
		return fmt.Errorf("state: update archive turn end: %w", err)
	}
	return nil
}

// ArchiveTurnArtifacts returns one turn's artifacts JSON.
func (s *Store) ArchiveTurnArtifacts(
	ctx context.Context, conversationID, runID string,
) ([]byte, bool, error) {
	query := `
		SELECT artifacts_json FROM archive_turns
		WHERE conversation_id = ?`
	args := []any{conversationID}
	if runID != "" {
		query += ` AND run_id = ?`
		args = append(args, runID)
	} else {
		query += ` ORDER BY seq DESC LIMIT 1`
	}
	var raw string
	err := s.db.SQLDB().QueryRowContext(ctx, query, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("state: load turn artifacts: %w", err)
	}
	return []byte(raw), true, nil
}

// DeleteConversationRows removes every state row owned by one
// conversation. Memory rows live in the same workspace DB and are
// registered by internal/foundation/compat; they are removed here too so
// a conversation deletion never leaves orphaned memory context
// behind. A store that has not run the workspace migrations yet has no
// such tables, and the cleanup is skipped for them.
func (s *Store) DeleteConversationRows(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("state: conversation id is required")
	}
	tx, err := s.db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin delete conversation: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "state: rollback delete conversation failed", err)
		}
	}()
	for _, stmt := range []string{
		`DELETE FROM archive_messages WHERE conversation_id = ?`,
		`DELETE FROM archive_turns WHERE conversation_id = ?`,
		// conversation_state holds every per-conversation document,
		// settings included: one statement removes the lot.
		`DELETE FROM conversation_state WHERE conversation_id = ?`,
		`DELETE FROM conversations WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			return fmt.Errorf("state: delete conversation %s: %w", id, err)
		}
	}
	// The summary tree is derived from these rows and would outlive them
	// (migration 020 retired the other derived copy, memory_items).
	for _, table := range []string{"summary_nodes"} {
		query := `DELETE FROM ` + table + ` WHERE thread_id = ?`
		if err := execIfTableExists(ctx, tx, table, query, id); err != nil {
			return fmt.Errorf("state: delete conversation %s memory: %w", id, err)
		}
	}
	// The message search index (migration 016) mirrors archive_messages
	// by row key; dropping the archive rows without it would leave hits
	// that join to nothing.
	if err := execIfTableExists(ctx, tx, "message_fts",
		`DELETE FROM message_fts WHERE conversation_id = ?`, id); err != nil {
		return fmt.Errorf("state: delete conversation %s search index: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit delete conversation %s: %w", id, err)
	}
	return nil
}

// execIfTableExists runs query only when table is present in the
// database. Workspace DBs get their complete schema registered by
// internal/foundation/compat, so standalone stores and tests may
// legitimately not have the tables yet.
func execIfTableExists(
	ctx context.Context,
	tx *sql.Tx,
	table, query string,
	args ...any,
) error {
	var found int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM sqlite_master
		WHERE type = 'table' AND name = ?`, table).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanConversation(row rowScanner) (Conversation, error) {
	var c Conversation
	var createdAt, updatedAt, usage string
	var ready int
	if err := row.Scan(&c.ID, &c.Title, &createdAt, &updatedAt,
		&c.TurnCount, &c.MessageCount, &usage, &c.ImportSource, &ready); err != nil {
		return Conversation{}, err
	}
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	c.UsageJSON = []byte(usage)
	c.ImportReady = ready != 0
	return c, nil
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		var fallbackErr error
		t, fallbackErr = time.Parse(time.RFC3339, s)
		if fallbackErr != nil {
			telemetry.WarnErr(context.Background(),
				"state: parse stored timestamp failed", fallbackErr)
		}
	}
	return t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
