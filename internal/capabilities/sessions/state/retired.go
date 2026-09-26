package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrRetired reports that a conversation id was deleted in this
// workspace and will not come back. Deleting a conversation records its
// id in deleted_conversations (workspace migration 021) inside the same
// transaction that removes the rows, so "this id was deleted" outlives
// the process. Every writer that could recreate the conversation — the
// transcript append, the start-title seed, a state document — refuses a
// retired id instead, which is what makes a delete final rather than
// final-until-something-writes-late.
//
// It is deliberately distinct from ErrNotFound: that one means "nothing
// was ever written under this id" and is a normal answer, while a
// retired id is a refusal the writer reports and a reader may turn into
// "this conversation was deleted".
var ErrRetired = errors.New("state: conversation was deleted")

// Retired reports whether one conversation id was deleted in this
// workspace. Callers use it where the difference between "never
// existed" and "was deleted" is what they owe a person: a client
// resuming a deleted conversation is refused by name instead of
// opening a chat that can never arrive.
func (s *Store) Retired(ctx context.Context, id string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("state: conversation id is required")
	}
	return retired(ctx, s.db.SQLDB(), id)
}

// rowQuerier is the read side of both the store handle and one
// transaction, so a guard inside a transaction asks the same question a
// public read does.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// retired reports whether id is recorded as deleted.
func retired(ctx context.Context, q rowQuerier, id string) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM deleted_conversations WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: read deleted conversation: %w", err)
	}
	return true, nil
}
