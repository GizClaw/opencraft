package state

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrCorruptDocument is the sentinel a caller classifies a broken
// conversation_state document with (errors.Is). A document exists but
// does not decode into the shape its owner expects; "never stored" is
// a different answer and reads as ErrNotFound.
var ErrCorruptDocument = errors.New("state: corrupt conversation state document")

// CorruptDocumentError names the one row that failed to decode. A
// document is written by one owner in one shape, so a decode failure is
// a data-level fact about that row: whoever reads the telemetry, and
// whoever decides what to fall back to, both need to know which
// conversation and which document it was.
type CorruptDocumentError struct {
	ConversationID string
	Name           string
	Err            error
}

func (e *CorruptDocumentError) Error() string {
	return fmt.Sprintf(
		"state: document %q of conversation %s is unreadable: %v",
		e.Name, e.ConversationID, e.Err)
}

// Unwrap exposes the JSON error underneath.
func (e *CorruptDocumentError) Unwrap() error { return e.Err }

// Is makes errors.Is(err, ErrCorruptDocument) work for the typed error.
func (e *CorruptDocumentError) Is(target error) bool {
	return target == ErrCorruptDocument
}

// DecodeDocument decodes one conversation_state document into v. It is
// the single place a stored document becomes a Go shape, so a document
// that cannot be decoded is reported the same way whichever reader
// found it — a *CorruptDocumentError naming the row — instead of a bare
// JSON error a fallback path can swallow without a trace.
func DecodeDocument(conversationID, name, raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return &CorruptDocumentError{
			ConversationID: conversationID,
			Name:           name,
			Err:            err,
		}
	}
	return nil
}
