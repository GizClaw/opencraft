package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// sqliteTurnStore is the memory-owned TurnStore over foundation/db.
type sqliteTurnStore struct {
	mu sync.Mutex
	db *db.DB
}

func (a *sqliteTurnStore) AppendMessages(
	ctx context.Context, conversationID, turnID string, msgs []message.Message,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	sqlDB := a.db.SQLDB()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin append: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			telemetry.WarnErr(ctx, "memory: rollback append failed", err)
		}
	}()
	if err := a.appendMessagesTx(ctx, tx, conversationID, turnID, msgs); err != nil {
		return err
	}
	return tx.Commit()
}

// appendMessagesTx inserts memory rows inside an existing transaction.
// It is used by the atomic archive+memory commit path.
func (a *sqliteTurnStore) appendMessagesTx(
	ctx context.Context,
	tx *sql.Tx,
	conversationID, turnID string,
	msgs []message.Message,
) error {
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), -1) + 1 FROM memory_items
		 WHERE thread_id = ?`, conversationID).Scan(&seq); err != nil {
		return fmt.Errorf("memory: next seq: %w", err)
	}
	for _, msg := range msgs {
		text := msg.Content.Text()
		if text == "" {
			if len(msg.Content.Parts) > 0 {
				// A message with parts but no rendered text would
				// otherwise vanish without a trace: the raw window and
				// the fold contract index text-bearing rows, so it
				// cannot be persisted here. Surface the drop instead of
				// silently losing history.
				telemetry.Warn(ctx, "memory: skipping message with parts but no rendered text",
					otellog.String("conversation.id", conversationID),
					otellog.String("turn.id", turnID),
					otellog.String("role", string(msg.Role)),
					otellog.Int("parts", len(msg.Content.Parts)))
			}
			continue
		}
		// Persist the full content (canonical parts), not just the text
		// projection: tool_call / tool_result parts carry the call ids
		// structured history replay needs. Text-only rows written by
		// older versions are upgraded to canonical parts by workspace
		// migration 011 on startup.
		payload, err := json.Marshal(msg.Content)
		if err != nil {
			return fmt.Errorf("memory: marshal message payload: %w", err)
		}
		id := conversationID + ":" + turnID + ":" + strconv.FormatInt(seq, 10)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memory_items(
				id, thread_id, turn_id, seq, item_type, role, payload, created_at
			) VALUES (?, ?, ?, ?, 'text', ?, ?, ?)`,
			id, conversationID, turnID, seq, string(msg.Role),
			string(payload), timeNow().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("memory: append message: %w", err)
		}
		seq++
	}
	return nil
}

// AppendMessagesTx appends memory rows inside the caller's transaction.
func (a *sqliteTurnStore) AppendMessagesTx(
	ctx context.Context,
	tx *sql.Tx,
	conversationID, turnID string,
	msgs []message.Message,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.appendMessagesTx(ctx, tx, conversationID, turnID, msgs)
}

// projectionBackfill is how many extra rows LoadBack may ask for beyond
// the rows it still needs. The SQL page already drops the cheap,
// structural non-history (system rows, app-authored turns); this covers
// the remaining case — a row the render step projects to nothing — so a
// page that loses rows to it still fills in one more query instead of
// returning a short window.
const projectionBackfill = 32

// MaxSeq returns the newest transcript seq, or -1 when the conversation
// has no rows.
func (a *sqliteTurnStore) MaxSeq(
	ctx context.Context, conversationID string,
) (int64, error) {
	var seq sql.NullInt64
	if err := a.db.SQLDB().QueryRowContext(ctx,
		`SELECT MAX(seq) FROM archive_messages WHERE conversation_id = ?`,
		conversationID).Scan(&seq); err != nil {
		return -1, fmt.Errorf("memory: max transcript seq: %w", err)
	}
	if !seq.Valid {
		return -1, nil
	}
	return seq.Int64, nil
}

// LoadBack returns the newest n replayable rows older than beforeSeq, in
// chronological order. It walks the transcript backward in pages, so a
// short window costs a bounded query rather than a scan of the
// conversation.
func (a *sqliteTurnStore) LoadBack(
	ctx context.Context, conversationID string, beforeSeq int64, n int,
) ([]summary.StoredMessage, error) {
	if n <= 0 || beforeSeq <= 0 {
		return nil, nil
	}
	var pages [][]summary.StoredMessage
	total := 0
	cursor := beforeSeq
	for total < n && cursor > 0 {
		limit := n - total + projectionBackfill
		page, err := a.loadProjectedBefore(ctx, conversationID, cursor, limit)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		pages = append(pages, page)
		total += len(page)
		cursor = page[0].Seq
		if len(page) < limit {
			// The page came up short: the transcript start was reached.
			break
		}
	}
	// pages are newest-batch-first, each in chronological order.
	var tail []summary.StoredMessage
	for i := len(pages) - 1; i >= 0; i-- {
		tail = append(tail, pages[i]...)
	}
	if len(tail) > n {
		tail = tail[len(tail)-n:]
	}
	return tail, nil
}

// LoadAll returns every replayable row, oldest first.
func (a *sqliteTurnStore) LoadAll(
	ctx context.Context, conversationID string,
) ([]summary.StoredMessage, error) {
	return a.loadProjectedBefore(ctx, conversationID, 0, 0)
}

// loadProjectedBefore reads the transcript backward from beforeSeq and
// returns the replayable rows among them in chronological order. limit
// bounds the rows read (0 means no bound); rows the projection leaves out
// do not count against it, so a caller asking for n rows may receive
// fewer if the transcript start was reached first.
func (a *sqliteTurnStore) loadProjectedBefore(
	ctx context.Context, conversationID string, beforeSeq int64, limit int,
) ([]summary.StoredMessage, error) {
	query := `SELECT m.seq, m.role, m.content_json, COALESCE(t.kind, '')
		FROM archive_messages m
		LEFT JOIN archive_turns t ON t.id = m.turn_id
		WHERE m.conversation_id = ? AND m.role != 'system'
			AND COALESCE(t.kind, '') = ''`
	args := []any{conversationID}
	if beforeSeq > 0 {
		query += ` AND m.seq < ?`
		args = append(args, beforeSeq)
	}
	query += ` ORDER BY m.seq DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := a.db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: load transcript rows: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "memory: close transcript rows failed", rows.Close())
	}()
	var desc []summary.StoredMessage
	for rows.Next() {
		var seq int64
		var role, payload, kind string
		if err := rows.Scan(&seq, &role, &payload, &kind); err != nil {
			return nil, fmt.Errorf("memory: scan transcript row: %w", err)
		}
		var content message.Content
		if err := json.Unmarshal([]byte(payload), &content); err != nil {
			telemetry.WarnErr(ctx, "memory: decode transcript payload failed", err,
				otellog.String("conversation.id", conversationID),
				otellog.Int64("seq", seq))
			continue
		}
		if len(content.Parts) == 0 {
			// Migration 011 rewrites legacy text-only rows to canonical
			// parts. A row that still has no parts (interrupted write,
			// foreign writer) is not part of the structured history and
			// is skipped here, with a warning, rather than failing the
			// turn or inventing content.
			telemetry.Warn(ctx, "memory: skipping part-less transcript row",
				otellog.String("conversation.id", conversationID),
				otellog.Int64("seq", seq))
			continue
		}
		projected, ok := projectRow(message.Role(role), content)
		if !ok {
			continue
		}
		desc = append(desc, summary.StoredMessage{
			Seq:     seq,
			Role:    message.Role(role),
			Content: projected.Content,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
		desc[i], desc[j] = desc[j], desc[i]
	}
	return desc, nil
}

func (a *sqliteTurnStore) UpsertSummaryNode(
	ctx context.Context, node summary.SummaryNode,
) error {
	parents, err := json.Marshal(node.ParentIDs)
	if err != nil {
		return fmt.Errorf("memory: marshal summary parents: %w", err)
	}
	sources, err := json.Marshal(node.SourceIDs)
	if err != nil {
		return fmt.Errorf("memory: marshal summary sources: %w", err)
	}
	metadata, err := json.Marshal(node.Metadata)
	if err != nil {
		return fmt.Errorf("memory: marshal summary metadata: %w", err)
	}
	content, err := json.Marshal(node.Content)
	if err != nil {
		return fmt.Errorf("memory: marshal summary content: %w", err)
	}
	_, err = a.db.SQLDB().ExecContext(ctx, `
		INSERT INTO summary_nodes(
			id, thread_id, level, parent_ids, source_ids, summary,
			created_at, updated_at, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			parent_ids = excluded.parent_ids,
			source_ids = excluded.source_ids,
			summary = excluded.summary,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata`,
		node.ID, node.ThreadID, node.Level,
		string(parents), string(sources), string(content),
		node.CreatedAt.UTC().Format(time.RFC3339Nano),
		node.UpdatedAt.UTC().Format(time.RFC3339Nano),
		string(metadata),
	)
	if err != nil {
		return fmt.Errorf("memory: upsert summary node: %w", err)
	}
	return nil
}

func (a *sqliteTurnStore) ListSummaryNodes(
	ctx context.Context, conversationID string,
) ([]summary.SummaryNode, error) {
	rows, err := a.db.SQLDB().QueryContext(ctx, `
		SELECT id, thread_id, level, parent_ids, source_ids, summary,
			created_at, updated_at, metadata
		FROM summary_nodes WHERE thread_id = ? ORDER BY level, created_at`,
		conversationID)
	if err != nil {
		return nil, fmt.Errorf("memory: list summary nodes: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "memory: close summary rows failed", rows.Close())
	}()
	var nodes []summary.SummaryNode
	for rows.Next() {
		var n summary.SummaryNode
		var parents, sources, content, createdAt, updatedAt, metadata string
		if err := rows.Scan(&n.ID, &n.ThreadID, &n.Level, &parents, &sources,
			&content, &createdAt, &updatedAt, &metadata); err != nil {
			return nil, fmt.Errorf("memory: scan summary node: %w", err)
		}
		if err := json.Unmarshal([]byte(parents), &n.ParentIDs); err != nil {
			telemetry.WarnErr(ctx, "memory: decode summary parents failed", err,
				otellog.String("conversation.id", conversationID))
		}
		if err := json.Unmarshal([]byte(sources), &n.SourceIDs); err != nil {
			telemetry.WarnErr(ctx, "memory: decode summary sources failed", err,
				otellog.String("conversation.id", conversationID))
		}
		if err := json.Unmarshal([]byte(content), &n.Content); err != nil {
			telemetry.WarnErr(ctx, "memory: decode summary content failed", err,
				otellog.String("conversation.id", conversationID))
		}
		if err := json.Unmarshal([]byte(metadata), &n.Metadata); err != nil {
			telemetry.WarnErr(ctx, "memory: decode summary metadata failed", err,
				otellog.String("conversation.id", conversationID))
		}
		var timeErr error
		n.CreatedAt, timeErr = time.Parse(time.RFC3339Nano, createdAt)
		if timeErr != nil {
			telemetry.WarnErr(ctx, "memory: parse summary created time failed",
				timeErr, otellog.String("conversation.id", conversationID))
		}
		n.UpdatedAt, timeErr = time.Parse(time.RFC3339Nano, updatedAt)
		if timeErr != nil {
			telemetry.WarnErr(ctx, "memory: parse summary updated time failed",
				timeErr, otellog.String("conversation.id", conversationID))
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (a *sqliteTurnStore) DeleteSummaryNodes(
	ctx context.Context, conversationID string, level int, keepID string,
) error {
	_, err := a.db.SQLDB().ExecContext(ctx, `
		DELETE FROM summary_nodes
		WHERE thread_id = ? AND level = ? AND id != ?`,
		conversationID, level, keepID)
	if err != nil {
		return fmt.Errorf("memory: delete summary nodes: %w", err)
	}
	return nil
}

// DeleteSummaryNodesByID removes the named nodes, which is how coverage
// written in another identity generation is retired.
func (a *sqliteTurnStore) DeleteSummaryNodesByID(
	ctx context.Context, conversationID string, ids []string,
) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]byte, 0, len(ids)*2)
	for i := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, conversationID)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := a.db.SQLDB().ExecContext(ctx, `
		DELETE FROM summary_nodes
		WHERE thread_id = ? AND id IN (`+string(placeholders)+`)`, args...)
	if err != nil {
		return fmt.Errorf("memory: delete summary nodes by id: %w", err)
	}
	return nil
}
