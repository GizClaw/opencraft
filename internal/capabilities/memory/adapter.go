package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/summary"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// RowsAfter projects the transcript rows appended after afterSeq, rendered the
// same way the replay renders them. It is the read the fold tool needs: the
// rows a range covers, so the tool never receives the conversation as an
// argument.
func RowsAfter(
	ctx context.Context, database *db.DB, conversationID string, afterSeq int64,
) ([]summary.StoredMessage, error) {
	return (&sqliteTurnStore{db: database}).LoadAfter(ctx, conversationID, afterSeq)
}

// sqliteTurnStore is the memory-owned TurnStore over foundation/db.
type sqliteTurnStore struct {
	db *db.DB
}

// projectionBackfill is how many extra rows LoadBack may ask for beyond
// the rows it still needs. The SQL page already drops the cheap,
// structural non-history (imported system rows); this covers the
// remaining case — a row the render step cannot project at all — so a
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
		if page.OldestRawSeq < 0 {
			// The read saw no rows at all: the transcript start was
			// reached.
			break
		}
		pages = append(pages, page.Messages)
		total += len(page.Messages)
		// The cursor steps over what the page *read*, not over what it
		// returned: the projection can leave every row of a page out,
		// and a page that returned nothing still has to move the walk
		// on rather than re-read itself.
		cursor = page.OldestRawSeq
		if !page.More {
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
	page, err := a.loadProjectedBefore(ctx, conversationID, 0, 0)
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

// transcriptPage is one backward read of the transcript: the rows the
// projection kept, plus what the read itself saw.
type transcriptPage struct {
	// Messages are the rows the projection kept, oldest first.
	Messages []summary.StoredMessage
	// OldestRawSeq is the smallest transcript seq the read saw, or -1
	// when it saw no rows at all.
	OldestRawSeq int64
	// More reports whether the read stopped at its own limit, i.e.
	// whether rows older than OldestRawSeq exist. The returned messages
	// cannot answer that: the projection may leave rows out, so a page
	// can come back short (or empty) with the transcript continuing
	// behind it.
	More bool
}

// loadProjectedBefore reads the transcript backward from beforeSeq and
// gathers the replayable rows among them in chronological order. limit
// bounds the rows read (0 means no bound); rows the projection leaves out
// do not count against it, which is why the page reports what it read
// alongside what it kept.
//
// What the projection did to those rows is reported once per query: a row
// that entered as a placeholder (an image-only turn) is a fact about the
// conversation the operator should be able to see, and a row that could
// not enter at all is a fact nobody should have to guess at.
func (a *sqliteTurnStore) loadProjectedBefore(
	ctx context.Context, conversationID string, beforeSeq int64, limit int,
) (transcriptPage, error) {
	// Every row of the conversation is a candidate, whatever wrote it:
	// a delegation note is the app speaking to the model and belongs in
	// the window like any other row. The one structural exclusion is an
	// imported conversation's own system prompt (see projection.go).
	query := `SELECT m.seq, m.role, m.content_json, m.prompt_json, m.prompt_state
		FROM archive_messages m
		WHERE m.conversation_id = ? AND m.role != 'system'`
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
		return transcriptPage{}, fmt.Errorf("memory: load transcript rows: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "memory: close transcript rows failed", rows.Close())
	}()
	projected, err := projectTranscriptRows(ctx, conversationID, rows)
	if err != nil {
		return transcriptPage{}, err
	}
	for i, j := 0, len(projected.Messages)-1; i < j; i, j = i+1, j-1 {
		projected.Messages[i], projected.Messages[j] =
			projected.Messages[j], projected.Messages[i]
	}
	return transcriptPage{
		Messages:     projected.Messages,
		OldestRawSeq: projected.OldestRawSeq,
		More:         limit > 0 && projected.Read == limit,
	}, nil
}

// projectedRows is one transcript read after the projection ran: the
// rows the projection kept, in the order the query returned them, plus
// what the read itself saw.
type projectedRows struct {
	// Messages are the rows the projection kept, in query order.
	Messages []summary.StoredMessage
	// Read is how many transcript rows the query returned.
	Read int
	// OldestRawSeq is the seq of the last row read, or -1 when the query
	// returned nothing. A backward read therefore reports the oldest row
	// it saw and a forward read the newest.
	OldestRawSeq int64
}

// projectTranscriptRows applies the projection to one transcript read.
// It is the only place a stored row becomes a model-visible message, so
// an incremental read (LoadAfter) and a full one (LoadAll) cannot drift:
// the query decides which rows are candidates, this decides what each of
// them becomes.
func projectTranscriptRows(
	ctx context.Context, conversationID string, rows *sql.Rows,
) (projectedRows, error) {
	var placeholders, undecodable, empty int
	out := projectedRows{OldestRawSeq: -1}
	for rows.Next() {
		var seq int64
		var role, payload, promptJSON, promptState string
		if err := rows.Scan(&seq, &role, &payload, &promptJSON, &promptState); err != nil {
			return projectedRows{},
				fmt.Errorf("memory: scan transcript row: %w", err)
		}
		out.Read++
		out.OldestRawSeq = seq
		// A row written since migration 026 carries the projection it was
		// stored with; the decode below is the upgrade path for older rows.
		if promptState != "" {
			if promptState == summarytext.PromptSkip {
				empty++
				continue
			}
			// The two columns hold different shapes: prompt_json is a whole
			// message (role + content), content_json is bare parts. Decoding
			// content_json as a message yields an empty message with a role —
			// which is how a transcript of blank turns reaches the provider.
			if promptState == summarytext.PromptPlaceholder {
				placeholders++
			}
			if promptJSON != "" {
				var stored message.Message
				if err := json.Unmarshal([]byte(promptJSON), &stored); err == nil {
					if stored.Role == "" {
						stored.Role = message.Role(role)
					}
					out.Messages = append(out.Messages, summary.StoredMessage{
						Seq:     seq,
						Role:    stored.Role,
						Content: stored.Content,
					})
					continue
				}
			} else {
				// No second copy: the projection is identical to the content,
				// so the content *is* the prompt form.
				var content message.Content
				if err := json.Unmarshal([]byte(payload), &content); err == nil {
					out.Messages = append(out.Messages, summary.StoredMessage{
						Seq:     seq,
						Role:    message.Role(role),
						Content: content,
					})
					continue
				}
			}
			// Fall through to the content projection: a prompt form that
			// does not decode is treated like a row written before the
			// column existed, not like a skipped row.
		}
		var content message.Content
		if err := json.Unmarshal([]byte(payload), &content); err != nil {
			// Canonical content carries at least one valid part, so a
			// legacy text-only row (migration 011's shape), an
			// interrupted write and a foreign writer all land here:
			// nothing can be shown for the row, and guessing at its
			// content would be worse than counting it.
			telemetry.WarnErr(ctx, "memory: decode transcript payload failed", err,
				attribute.String("conversation.id", conversationID),
				attribute.Int64("seq", seq))
			undecodable++
			continue
		}
		projected, ok := projectRow(message.Role(role), content)
		if !ok {
			// Decodable content that has no prompt form: a
			// signature-only reasoning trace is the shape that reaches
			// this, and there is nothing of it a prompt can carry.
			empty++
			continue
		}
		if projected.Placeholder {
			placeholders++
		}
		out.Messages = append(out.Messages, summary.StoredMessage{
			Seq:     seq,
			Role:    message.Role(role),
			Content: projected.Message.Content,
		})
	}
	if err := rows.Err(); err != nil {
		return projectedRows{}, err
	}
	if placeholders > 0 {
		telemetry.Info(ctx,
			"memory: transcript rows rendered as placeholders",
			attribute.String("conversation.id", conversationID),
			attribute.Int("rows", placeholders))
	}
	if undecodable+empty > 0 {
		telemetry.Warn(ctx, "memory: transcript rows left out of the window",
			attribute.String("conversation.id", conversationID),
			attribute.Int("undecodable", undecodable),
			attribute.Int("unrenderable", empty))
	}
	return out, nil
}

// LoadAfter returns the replayable rows appended after afterSeq, oldest
// first: the incremental half of LoadAll, for a reader that already holds
// the prefix. The transcript is append-only, so the same projection over
// the rows after a cursor yields exactly the rows a full read would have
// produced past it.
func (a *sqliteTurnStore) LoadAfter(
	ctx context.Context, conversationID string, afterSeq int64,
) ([]summary.StoredMessage, error) {
	rows, err := a.db.SQLDB().QueryContext(ctx, `
		SELECT m.seq, m.role, m.content_json, m.prompt_json, m.prompt_state
		FROM archive_messages m
		WHERE m.conversation_id = ? AND m.role != 'system' AND m.seq > ?
		ORDER BY m.seq`, conversationID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("memory: load transcript rows after %d: %w",
			afterSeq, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "memory: close transcript rows failed", rows.Close())
	}()
	projected, err := projectTranscriptRows(ctx, conversationID, rows)
	if err != nil {
		return nil, err
	}
	return projected.Messages, nil
}
