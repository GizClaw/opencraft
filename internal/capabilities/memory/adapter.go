package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

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
	defer func() { _ = tx.Rollback() }()
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
			continue
		}
		payload, _ := json.Marshal(map[string]any{"text": text})
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

func (a *sqliteTurnStore) LoadMessages(
	ctx context.Context, conversationID string,
) ([]message.Message, error) {
	return a.loadRange(ctx, conversationID, -1, -1)
}

func (a *sqliteTurnStore) CountMessages(
	ctx context.Context, conversationID string,
) (int, error) {
	var n int
	if err := a.db.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_items WHERE thread_id = ?`,
		conversationID).Scan(&n); err != nil {
		return 0, fmt.Errorf("memory: count messages: %w", err)
	}
	return n, nil
}

func (a *sqliteTurnStore) LoadMessagesRange(
	ctx context.Context, conversationID string, from, to int,
) ([]message.Message, error) {
	return a.loadRange(ctx, conversationID, from, to)
}

func (a *sqliteTurnStore) loadRange(
	ctx context.Context, conversationID string, from, to int,
) ([]message.Message, error) {
	if from > to {
		return nil, nil
	}
	query := `SELECT role, payload FROM memory_items WHERE thread_id = ?`
	args := []any{conversationID}
	if from >= 0 && to >= from {
		query += ` AND seq BETWEEN ? AND ?`
		args = append(args, from, to)
	}
	query += ` ORDER BY seq`
	rows, err := a.db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: load messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []message.Message
	for rows.Next() {
		var role, payload string
		if err := rows.Scan(&role, &payload); err != nil {
			return nil, fmt.Errorf("memory: scan message: %w", err)
		}
		var obj struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(payload), &obj)
		if obj.Text == "" {
			continue
		}
		out = append(out, message.NewTextMessage(message.Role(role), obj.Text))
	}
	return out, rows.Err()
}

func (a *sqliteTurnStore) UpsertSummaryNode(
	ctx context.Context, node summary.SummaryNode,
) error {
	parents, _ := json.Marshal(node.ParentIDs)
	sources, _ := json.Marshal(node.SourceIDs)
	metadata, _ := json.Marshal(node.Metadata)
	content, _ := json.Marshal(node.Content)
	_, err := a.db.SQLDB().ExecContext(ctx, `
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
	defer func() { _ = rows.Close() }()
	var nodes []summary.SummaryNode
	for rows.Next() {
		var n summary.SummaryNode
		var parents, sources, content, createdAt, updatedAt, metadata string
		if err := rows.Scan(&n.ID, &n.ThreadID, &n.Level, &parents, &sources,
			&content, &createdAt, &updatedAt, &metadata); err != nil {
			return nil, fmt.Errorf("memory: scan summary node: %w", err)
		}
		_ = json.Unmarshal([]byte(parents), &n.ParentIDs)
		_ = json.Unmarshal([]byte(sources), &n.SourceIDs)
		_ = json.Unmarshal([]byte(content), &n.Content)
		_ = json.Unmarshal([]byte(metadata), &n.Metadata)
		n.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		n.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
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
