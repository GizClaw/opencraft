// Full-text search over archived messages (the session_search tool).
//
// The index table (migration 016, message_fts) holds one row per
// archive_messages row, keyed by that row's id and written inside the
// same transaction as the archive row itself, so the index can never
// drift from the archive. The indexed text is the message's prompt
// projection rendered by Go (text parts plus tool_call / tool_result
// lines), not the stored JSON: a snippet must read like the
// conversation and a query must match what the message says.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

const (
	// maxIndexRunes bounds the text one message contributes to the
	// index. The trigram index is a few times the size of the text it
	// covers, and a search only ever needs a window of a message: the
	// head of a huge tool result already carries the truncation pointer
	// the result middleware wrote, and the excerpt above it.
	maxIndexRunes = 4000
	// snippetTokens is the snippet window handed to fts5's snippet().
	// The trigram tokenizer counts three-character windows, so a token
	// budget is roughly a character budget: 200 keeps a snippet around
	// 200 characters.
	snippetTokens = 200
	// minTrigramRunes is the shortest query the trigram index can
	// match: shorter queries fall back to a substring scan.
	minTrigramRunes = 3
	// defaultSearchLimit / maxSearchLimit bound one search.
	defaultSearchLimit = 8
	maxSearchLimit     = 10
	// collapseOverfetch is how many ranked messages are read per
	// requested conversation when collapsing: enough that the best
	// snippet of each matching conversation is found without scanning
	// the whole result set.
	collapseOverfetch = 5
	// atLayout is the timestamp layout archive rows use.
	atLayout = time.RFC3339Nano
)

// SearchOptions bounds one message search.
type SearchOptions struct {
	// Limit caps the returned hits (default 8, at most 10). With
	// Collapse set it caps conversations instead of messages.
	Limit int
	// ConversationID restricts the search to one conversation.
	ConversationID string
	// Since drops messages older than the given instant.
	Since time.Time
	// Collapse keeps at most one hit per conversation: the best-ranked
	// message, so one chatty session cannot fill the result list.
	Collapse bool
}

// SearchHit is one message matched by a full-text search.
type SearchHit struct {
	ConversationID string
	Title          string
	RunID          string
	Role           string
	At             time.Time
	// TurnSeq is the archived turn's sequence within the conversation;
	// Seq is the message's sequence within the conversation. The pair
	// locates the message for a later read.
	TurnSeq int
	Seq     int
	Snippet string
}

// SearchResult is one search response. Truncated reports that more
// matches exist than were returned; Substring reports that the query
// was too short for the trigram index and the scan fell back to a
// plain substring match.
type SearchResult struct {
	Hits      []SearchHit
	Truncated bool
	Substring bool
}

// SearchMessages searches every archived message of the workspace.
func (s *Store) SearchMessages(
	ctx context.Context, query string, opts SearchOptions,
) (SearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return SearchResult{},
			errdefs.Validationf("state: search query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	// Read one row more than needed so "there may be more" is known
	// without a second query.
	fetch := limit
	if opts.Collapse {
		fetch = limit * collapseOverfetch
	}
	hits, indexed, err := s.searchHits(ctx, trimmed, opts, fetch+1)
	if err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Substring: !indexed}
	if len(hits) > fetch {
		hits = hits[:fetch]
		result.Truncated = true
	}
	if opts.Collapse {
		hits = collapseHits(hits)
	}
	if len(hits) > limit {
		hits = hits[:limit]
		result.Truncated = true
	}
	result.Hits = hits
	return result, nil
}

// collapseHits keeps the first (best-ranked) hit of each conversation.
func collapseHits(hits []SearchHit) []SearchHit {
	seen := make(map[string]bool, len(hits))
	folded := hits[:0]
	for _, hit := range hits {
		if seen[hit.ConversationID] {
			continue
		}
		seen[hit.ConversationID] = true
		folded = append(folded, hit)
	}
	return folded
}

// searchHits runs one ranked search. indexed reports whether the match
// used the trigram index (false: the query fell back to a substring
// scan).
func (s *Store) searchHits(
	ctx context.Context, query string, opts SearchOptions, limit int,
) ([]SearchHit, bool, error) {
	// The trigram index cannot match a term shorter than three
	// characters, so those terms filter with LIKE instead — and a query
	// made only of such terms is a plain substring scan. Quoting makes
	// every other term a literal phrase, so punctuation in the query
	// cannot turn into FTS5 syntax.
	phrases, short := splitMatchTerms(query)
	indexed := len(phrases) > 0

	var b strings.Builder
	b.WriteString(`SELECT message_fts.conversation_id, message_fts.role, `)
	b.WriteString(`message_fts.at, `)
	if indexed {
		fmt.Fprintf(&b, "snippet(message_fts, 3, '[', ']', '…', %d), ", snippetTokens)
	} else {
		b.WriteString("message_fts.text, ")
	}
	b.WriteString(`COALESCE(conversations.title, ''), `)
	b.WriteString(`COALESCE(archive_turns.run_id, ''), `)
	b.WriteString(`archive_turns.seq, archive_messages.seq
		FROM message_fts
		JOIN archive_messages ON archive_messages.id = message_fts.rowid
		JOIN archive_turns ON archive_turns.id = archive_messages.turn_id
		LEFT JOIN conversations ON conversations.id = message_fts.conversation_id`)

	var (
		conds []string
		args  []any
	)
	if indexed {
		conds = append(conds, "message_fts MATCH ?")
		args = append(args, strings.Join(phrases, " "))
	}
	for _, term := range short {
		conds = append(conds, `message_fts.text LIKE '%' || ? || '%' ESCAPE '\'`)
		args = append(args, escapeLike(term))
	}
	if opts.ConversationID != "" {
		conds = append(conds, "message_fts.conversation_id = ?")
		args = append(args, opts.ConversationID)
	}
	if !opts.Since.IsZero() {
		conds = append(conds, "julianday(message_fts.at) >= julianday(?)")
		args = append(args, opts.Since.UTC().Format(atLayout))
	}
	b.WriteString(" WHERE ")
	b.WriteString(strings.Join(conds, " AND "))
	// julianday() rather than the stored text: lexical order breaks
	// between timestamps that differ in fractional-second precision.
	b.WriteString(" ORDER BY ")
	if indexed {
		b.WriteString("bm25(message_fts), julianday(message_fts.at) DESC")
	} else {
		b.WriteString("julianday(message_fts.at) DESC")
	}
	b.WriteString(" LIMIT ?")
	args = append(args, limit)

	rows, err := s.db.SQLDB().QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, indexed, fmt.Errorf("state: search messages: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "state: close search rows failed", rows.Close())
	}()
	var hits []SearchHit
	for rows.Next() {
		var (
			hit     SearchHit
			at      string
			snippet string
		)
		if err := rows.Scan(
			&hit.ConversationID, &hit.Role, &at, &snippet,
			&hit.Title, &hit.RunID, &hit.TurnSeq, &hit.Seq,
		); err != nil {
			return nil, indexed, fmt.Errorf("state: scan search hit: %w", err)
		}
		hit.Snippet = snippet
		if !indexed {
			// No snippet() without MATCH: excerpt the stored text.
			hit.Snippet = excerptText(snippet, snippetTokens)
		}
		parsed, err := time.Parse(atLayout, at)
		if err != nil {
			telemetry.WarnErr(ctx, "state: search hit timestamp unreadable", err,
				otellog.String("conversation.id", hit.ConversationID))
		} else {
			hit.At = parsed
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, indexed, fmt.Errorf("state: read search hits: %w", err)
	}
	return hits, indexed, nil
}

// splitMatchTerms splits a query into the terms the trigram index can
// match (three or more characters, quoted as literal phrases) and the
// shorter ones, which the caller filters with LIKE. A query with no
// matchable term reports no phrases, i.e. a pure substring scan.
func splitMatchTerms(query string) (phrases, short []string) {
	for _, term := range strings.Fields(query) {
		if utf8.RuneCountInString(term) >= minTrigramRunes {
			phrases = append(phrases,
				`"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
			continue
		}
		short = append(short, term)
	}
	return phrases, short
}

// escapeLike escapes LIKE's wildcards so a term matches literally. The
// caller pairs it with ESCAPE '\'.
func escapeLike(term string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
}

// indexMessageText renders the text the index stores for one message:
// the canonical prompt projection, bounded so a huge message cannot
// dominate the index.
func indexMessageText(content message.Content) string {
	text := strings.TrimSpace(summarytext.RenderMessage(
		message.Message{Content: content}))
	return truncateIndexText(text)
}

// insertMessageFTS writes one index row for the archive message rowid
// inside the caller's transaction. Empty text has nothing to index and
// is skipped, which the backfill handles by never re-reading a row it
// has already passed.
func insertMessageFTS(
	ctx context.Context, e execer,
	conversationID, role, at string, rowid int64, text string,
) error {
	if text == "" {
		return nil
	}
	if _, err := e.ExecContext(ctx, `
		INSERT INTO message_fts(rowid, conversation_id, role, at, text)
		VALUES (?, ?, ?, ?, ?)`,
		rowid, conversationID, role, at, text,
	); err != nil {
		return fmt.Errorf("state: index message %d: %w", rowid, err)
	}
	return nil
}

// execer is the SQL surface shared by the database handle and a
// transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// BackfillSearchIndex indexes every archived message the full-text
// index does not hold yet. It is the migration-17 step's implementation
// (see foundation/compat) and idempotent by construction: it walks
// archive rows by id, skipping both rows already indexed and rows with
// no text to index, so an interrupted run resumes without duplicates.
func (s *Store) BackfillSearchIndex(ctx context.Context) error {
	const batch = 500
	lastID := int64(0)
	for {
		rows, err := s.db.SQLDB().QueryContext(ctx, `
			SELECT m.id, m.conversation_id, m.role, m.created_at, m.content_json
			FROM archive_messages m
			WHERE m.id > ?
			  AND NOT EXISTS (SELECT 1 FROM message_fts f WHERE f.rowid = m.id)
			ORDER BY m.id
			LIMIT ?`, lastID, batch)
		if err != nil {
			return fmt.Errorf("state: scan messages to index: %w", err)
		}
		var pending []indexRow
		for rows.Next() {
			var row indexRow
			if err := rows.Scan(
				&row.id, &row.conv, &row.role, &row.at, &row.content,
			); err != nil {
				closeRows(ctx, rows)
				return fmt.Errorf("state: scan message to index: %w", err)
			}
			pending = append(pending, row)
		}
		if err := rows.Err(); err != nil {
			closeRows(ctx, rows)
			return fmt.Errorf("state: read messages to index: %w", err)
		}
		closeRows(ctx, rows)
		if len(pending) == 0 {
			return nil
		}
		if err := s.indexMessageBatch(ctx, pending); err != nil {
			return err
		}
		for _, row := range pending {
			lastID = row.id
		}
	}
}

// indexRow is one archive row waiting for its index row.
type indexRow struct {
	id      int64
	conv    string
	role    string
	at      string
	content string
}

// indexMessageBatch writes one batch of backfill rows in a single
// transaction.
func (s *Store) indexMessageBatch(ctx context.Context, rows []indexRow) error {
	tx, err := s.db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin index batch: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			telemetry.WarnErr(ctx, "state: rollback index batch failed", err)
		}
	}()
	for _, row := range rows {
		var content message.Content
		if err := json.Unmarshal([]byte(row.content), &content); err != nil {
			// A row written by a foreign or future writer stays
			// searchable for nothing rather than failing the
			// migration: it is skipped like memory's window does.
			telemetry.WarnErr(ctx, "state: skip undecodable message while indexing",
				err, otellog.Int64("message.id", row.id))
			continue
		}
		if err := insertMessageFTS(ctx, tx, row.conv, row.role, row.at,
			row.id, indexMessageText(content)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit index batch: %w", err)
	}
	return nil
}

// closeRows closes a scan in a best-effort way, reporting a failure
// that is otherwise invisible.
func closeRows(ctx context.Context, rows *sql.Rows) {
	telemetry.WarnErr(ctx, "state: close index scan failed", rows.Close())
}

// truncateIndexText bounds the indexed text, marking the cut.
func truncateIndexText(text string) string {
	if utf8.RuneCountInString(text) <= maxIndexRunes {
		return text
	}
	return strings.TrimRight(cutRunes(text, maxIndexRunes), " \n\t") + " …"
}

// excerptText renders the snippet for the substring fallback, where fts5
// has no match to center on: the head of the text, bounded.
func excerptText(text string, runes int) string {
	return truncateIndexText(strings.TrimSpace(cutRunes(text, runes)))
}

// cutRunes returns s truncated to at most n runes, never inside one.
func cutRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
