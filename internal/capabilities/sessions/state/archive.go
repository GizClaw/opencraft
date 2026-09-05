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
	ID            int64
	Seq           int
	RunID         string
	At            time.Time
	RequestedAt   time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	Status        string
	Error         string
	ArtifactsJSON []byte
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

// CommitHook runs inside the conversation turn transaction after the
// archive rows have been written. It lets the memory owner append its
// rows atomically with the archive.
type CommitHook func(ctx context.Context, tx *sql.Tx) error

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

// CommitArchiveTurn atomically appends one full-fidelity turn and its
// messages.
func (s *Store) CommitConversationTurn(
	ctx context.Context,
	c Conversation,
	turn ArchiveTurn,
	msgs []ArchiveMessage,
) error {
	return s.CommitConversationTurnWithHook(ctx, c, turn, msgs, nil)
}

// CommitConversationTurnWithHook atomically appends one full-fidelity
// turn, its messages, and any memory rows the hook writes.
func (s *Store) CommitConversationTurnWithHook(
	ctx context.Context,
	c Conversation,
	turn ArchiveTurn,
	msgs []ArchiveMessage,
	hook CommitHook,
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
			status, error, artifacts_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, turnSeq, runID,
		turn.At.UTC().Format(time.RFC3339Nano),
		turn.RequestedAt.UTC().Format(time.RFC3339Nano),
		turn.StartedAt.UTC().Format(time.RFC3339Nano),
		turn.FinishedAt.UTC().Format(time.RFC3339Nano),
		turn.Status,
		turn.Error,
		string(turn.ArtifactsJSON),
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO archive_messages(
				conversation_id, turn_id, seq, role, content_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, turnID, msgs[i].Seq, msgs[i].Role,
			string(content),
			msgs[i].CreatedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("state: insert archive message: %w", err)
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
	if hook != nil {
		if err := hook(ctx, tx); err != nil {
			return fmt.Errorf("state: conversation turn hook: %w", err)
		}
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
	rows, err := s.db.SQLDB().QueryContext(ctx, `
		SELECT id, conversation_id, seq, run_id, at,
			requested_at, started_at, finished_at,
			status, error, artifacts_json
		FROM archive_turns WHERE conversation_id = ? ORDER BY seq`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("state: list archive turns: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close archive turn rows failed", rows.Close())
	}()
	var out []ArchiveTurn
	for rows.Next() {
		var t ArchiveTurn
		var conversationID string
		var runID sql.NullString
		var at, requested, started, finished, status, errText, artifacts string
		if err := rows.Scan(&t.ID, &conversationID, &t.Seq, &runID, &at,
			&requested, &started, &finished, &status, &errText, &artifacts); err != nil {
			return nil, fmt.Errorf("state: scan archive turn: %w", err)
		}
		t.RunID = runID.String
		t.At = parseTime(at)
		t.RequestedAt = parseTime(requested)
		t.StartedAt = parseTime(started)
		t.FinishedAt = parseTime(finished)
		t.Status = status
		t.Error = errText
		t.ArtifactsJSON = []byte(artifacts)
		out = append(out, t)
	}
	return out, rows.Err()
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
		var m ArchiveMessage
		var content, createdAt string
		if err := rows.Scan(&m.ID, &m.TurnID, &m.Seq, &m.Role,
			&content, &createdAt); err != nil {
			return nil, fmt.Errorf("state: scan archive message: %w", err)
		}
		if err := json.Unmarshal([]byte(content), &m.Content); err != nil {
			return nil, fmt.Errorf("state: decode archive message: %w", err)
		}
		m.CreatedAt = parseTime(createdAt)
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
	var t ArchiveTurn
	var convID string
	var run sql.NullString
	var at, requested, started, finished, status, errText, artifacts string
	err := s.db.SQLDB().QueryRowContext(ctx, `
		SELECT id, conversation_id, seq, run_id, at,
			requested_at, started_at, finished_at,
			status, error, artifacts_json
		FROM archive_turns
		WHERE conversation_id = ? AND run_id = ?`,
		conversationID, runID,
	).Scan(&t.ID, &convID, &t.Seq, &run, &at,
		&requested, &started, &finished, &status, &errText, &artifacts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArchiveTurn{}, nil, ErrNotFound
		}
		return ArchiveTurn{}, nil,
			fmt.Errorf("state: get archive turn by run: %w", err)
	}
	t.RunID = run.String
	t.At = parseTime(at)
	t.RequestedAt = parseTime(requested)
	t.StartedAt = parseTime(started)
	t.FinishedAt = parseTime(finished)
	t.Status = status
	t.Error = errText
	t.ArtifactsJSON = []byte(artifacts)

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
		var m ArchiveMessage
		var content, createdAt string
		if err := rows.Scan(&m.ID, &m.TurnID, &m.Seq, &m.Role,
			&content, &createdAt); err != nil {
			return ArchiveTurn{}, nil,
				fmt.Errorf("state: scan archive turn message: %w", err)
		}
		if err := json.Unmarshal([]byte(content), &m.Content); err != nil {
			return ArchiveTurn{}, nil,
				fmt.Errorf("state: decode archive turn message: %w", err)
		}
		m.CreatedAt = parseTime(createdAt)
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
	finishedAt time.Time, status, errText string,
) error {
	if runID == "" {
		return fmt.Errorf("state: run id is required")
	}
	finishedAt = finishedAt.UTC()
	_, err := s.db.SQLDB().ExecContext(ctx, `
		UPDATE archive_turns SET
			finished_at = ?,
			status = ?,
			error = ?
		WHERE conversation_id = ? AND run_id = ?`,
		finishedAt.Format(time.RFC3339Nano),
		status, errText, conversationID, runID)
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

// UpdateArchiveTurnArtifacts replaces one turn's artifacts JSON.
func (s *Store) UpdateArchiveTurnArtifacts(
	ctx context.Context, conversationID, runID string, artifacts []byte,
) error {
	if runID == "" {
		return fmt.Errorf("state: run id is required")
	}
	if len(artifacts) == 0 {
		artifacts = []byte("[]")
	}
	_, err := s.db.SQLDB().ExecContext(ctx, `
		UPDATE archive_turns SET artifacts_json = ?
		WHERE conversation_id = ? AND run_id = ?`,
		string(artifacts), conversationID, runID)
	if err != nil {
		return fmt.Errorf("state: update turn artifacts: %w", err)
	}
	return nil
}

// DeleteConversationRows removes every state row owned by one
// conversation. Memory rows live in the same workspace DB and are
// registered by orchestration/migrations; they are removed here too so
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
		`DELETE FROM conversation_state WHERE conversation_id = ?`,
		`DELETE FROM session_settings WHERE context_id = ?`,
		`DELETE FROM conversations WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			return fmt.Errorf("state: delete conversation %s: %w", id, err)
		}
	}
	for _, table := range []string{"memory_items", "summary_nodes"} {
		query := `DELETE FROM ` + table + ` WHERE thread_id = ?`
		if err := execIfTableExists(ctx, tx, table, query, id); err != nil {
			return fmt.Errorf("state: delete conversation %s memory: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit delete conversation %s: %w", id, err)
	}
	return nil
}

// execIfTableExists runs query only when table is present in the
// database. Workspace DBs get their complete schema registered by
// orchestration/migrations, so standalone stores and tests may
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
