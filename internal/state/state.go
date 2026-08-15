// Package state implements opencraft's default SQLite storage: threads,
// turns, items, summary nodes, and schema migrations.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	_ "modernc.org/sqlite" // registers the "sqlite" driver.
)

// Store is a SQLite-backed opencraft state store. Use Open to create or
// open the database; the store owns the handle.
type Store struct {
	db *sql.DB
}

// Thread is one conversation thread.
type Thread struct {
	ID             string
	AgentID        string
	ContextID      string
	Title          string
	ParentThreadID string
	Status         string // active | archived | deleted
	Metadata       map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Turn is one execution unit within a thread.
type Turn struct {
	ID         string
	ThreadID   string
	RunID      string
	Seq        int
	Status     string // running | completed | interrupted | failed
	Model      string
	StartedAt  time.Time
	FinishedAt *time.Time
	Metadata   map[string]any
}

// Item is one persisted conversation/tool item.
type Item struct {
	ID        string
	ThreadID  string
	TurnID    string
	Seq       int64
	ItemType  string // reasoning | text | tool_call | tool_result | shell | file_edit | web_search
	Role      string // user | assistant | developer
	Payload   map[string]any
	CreatedAt time.Time
}

// SummaryNode is a memory-summary node (see internal/memory/summary).
type SummaryNode struct {
	ID        string
	ThreadID  string
	Level     int
	ParentIDs []string
	SourceIDs []string
	Content   message.Content
	CreatedAt time.Time
	UpdatedAt time.Time
	Metadata  map[string]any
}

// Open opens (creating if necessary) the SQLite database at path,
// applies migrations, and returns a Store that owns the handle.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("state: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: busy timeout: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrate applies versioned, idempotent schema migrations.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("state: create migrations table: %w", err)
	}

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			context_id TEXT NOT NULL,
			title TEXT,
			parent_thread_id TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_threads_agent ON threads(agent_id, context_id);
		CREATE INDEX IF NOT EXISTS idx_threads_status ON threads(status);`,
		`CREATE TABLE IF NOT EXISTS turns (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL REFERENCES threads(id),
			run_id TEXT,
			seq INTEGER NOT NULL,
			status TEXT NOT NULL,
			model TEXT,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			metadata TEXT NOT NULL DEFAULT '{}'
		);
		CREATE INDEX IF NOT EXISTS idx_turns_thread ON turns(thread_id, seq);
		CREATE INDEX IF NOT EXISTS idx_turns_run ON turns(run_id);`,
		`CREATE TABLE IF NOT EXISTS items (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL REFERENCES threads(id),
			turn_id TEXT NOT NULL REFERENCES turns(id),
			seq INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			role TEXT,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_items_thread_seq ON items(thread_id, seq);
		CREATE INDEX IF NOT EXISTS idx_items_turn ON items(turn_id);
		CREATE INDEX IF NOT EXISTS idx_items_type ON items(thread_id, item_type);`,
		`CREATE TABLE IF NOT EXISTS summary_nodes (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL REFERENCES threads(id),
			level INTEGER NOT NULL DEFAULT 0,
			parent_ids TEXT NOT NULL DEFAULT '[]',
			source_ids TEXT NOT NULL DEFAULT '[]',
			summary TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}'
		);
		CREATE INDEX IF NOT EXISTS idx_summary_thread_level ON summary_nodes(thread_id, level);`,
	}
	for i, stmt := range migrations {
		version := i + 1
		var exists int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
		).Scan(&exists); err != nil {
			return fmt.Errorf("state: check migration %d: %w", version, err)
		}
		if exists > 0 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("state: begin migration %d: %w", version, err)
		}
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("state: apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("state: record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("state: commit migration %d: %w", version, err)
		}
	}
	return nil
}

// CreateThread inserts a thread.
func (s *Store) CreateThread(ctx context.Context, t Thread) error {
	metadata, err := json.Marshal(t.Metadata)
	if err != nil {
		return fmt.Errorf("state: thread metadata: %w", err)
	}
	if t.Status == "" {
		t.Status = "active"
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO threads(
		id, agent_id, context_id, title, parent_thread_id, status, metadata, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.AgentID, t.ContextID, t.Title, t.ParentThreadID,
		t.Status, string(metadata), t.CreatedAt.UTC().Format(time.RFC3339),
		t.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("state: create thread: %w", err)
	}
	return nil
}

// AppendItem appends one item to a thread.
func (s *Store) AppendItem(ctx context.Context, item Item) error {
	payload, err := json.Marshal(item.Payload)
	if err != nil {
		return fmt.Errorf("state: item payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO items(
		id, thread_id, turn_id, seq, item_type, role, payload, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.ThreadID, item.TurnID, item.Seq, item.ItemType,
		item.Role, string(payload), item.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("state: append item: %w", err)
	}
	return nil
}

// LoadItems returns all items of a thread ordered by seq.
func (s *Store) LoadItems(ctx context.Context, threadID string) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, turn_id, seq,
		item_type, role, payload, created_at FROM items
		WHERE thread_id = ? ORDER BY seq`, threadID)
	if err != nil {
		return nil, fmt.Errorf("state: load items: %w", err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		var payload, createdAt string
		if err := rows.Scan(&it.ID, &it.ThreadID, &it.TurnID, &it.Seq,
			&it.ItemType, &it.Role, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("state: scan item: %w", err)
		}
		it.Payload = map[string]any{}
		if err := json.Unmarshal([]byte(payload), &it.Payload); err != nil {
			return nil, fmt.Errorf("state: decode item payload: %w", err)
		}
		it.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		items = append(items, it)
	}
	return items, rows.Err()
}

// UpsertSummaryNode inserts or replaces one summary node by id.
func (s *Store) UpsertSummaryNode(ctx context.Context, n SummaryNode) error {
	parents, err := json.Marshal(n.ParentIDs)
	if err != nil {
		return fmt.Errorf("state: parent ids: %w", err)
	}
	sources, err := json.Marshal(n.SourceIDs)
	if err != nil {
		return fmt.Errorf("state: source ids: %w", err)
	}
	metadata, err := json.Marshal(n.Metadata)
	if err != nil {
		return fmt.Errorf("state: summary metadata: %w", err)
	}
	content, err := json.Marshal(n.Content)
	if err != nil {
		return fmt.Errorf("state: summary content: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO summary_nodes(
		id, thread_id, level, parent_ids, source_ids, summary, created_at, updated_at, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		parent_ids = excluded.parent_ids,
		source_ids = excluded.source_ids,
		summary = excluded.summary,
		created_at = excluded.created_at,
		updated_at = excluded.updated_at,
		metadata = excluded.metadata`,
		n.ID, n.ThreadID, n.Level, string(parents), string(sources), string(content),
		n.CreatedAt.UTC().Format(time.RFC3339), n.UpdatedAt.UTC().Format(time.RFC3339),
		string(metadata),
	)
	if err != nil {
		return fmt.Errorf("state: upsert summary node: %w", err)
	}
	return nil
}

// ListSummaryNodes returns summary nodes of a thread ordered by level and
// creation time.
func (s *Store) ListSummaryNodes(ctx context.Context, threadID string) ([]SummaryNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, level, parent_ids,
		source_ids, summary, created_at, updated_at, metadata
		FROM summary_nodes WHERE thread_id = ? ORDER BY level, created_at`, threadID)
	if err != nil {
		return nil, fmt.Errorf("state: list summary nodes: %w", err)
	}
	defer rows.Close()
	var nodes []SummaryNode
	for rows.Next() {
		var n SummaryNode
		var parents, sources, summary, createdAt, updatedAt, metadata string
		if err := rows.Scan(&n.ID, &n.ThreadID, &n.Level, &parents, &sources,
			&summary, &createdAt, &updatedAt, &metadata); err != nil {
			return nil, fmt.Errorf("state: scan summary node: %w", err)
		}
		_ = json.Unmarshal([]byte(parents), &n.ParentIDs)
		_ = json.Unmarshal([]byte(sources), &n.SourceIDs)
		_ = json.Unmarshal([]byte(metadata), &n.Metadata)
		_ = json.Unmarshal([]byte(summary), &n.Content)
		n.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		n.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// DeleteSummaryNodes removes summary nodes of a thread at a level, keeping
// the node whose id equals keepID (pass "" to delete all at that level).
// It backs the level-0 rolling summary, which replaces its previous node in
// place instead of accumulating rows per fold.
func (s *Store) DeleteSummaryNodes(ctx context.Context, threadID string, level int, keepID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM summary_nodes WHERE thread_id = ? AND level = ? AND id != ?`,
		threadID, level, keepID)
	if err != nil {
		return fmt.Errorf("state: delete summary nodes: %w", err)
	}
	return nil
}

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("state: not found")
