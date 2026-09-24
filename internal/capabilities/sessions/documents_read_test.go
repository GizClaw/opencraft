package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestReadStateReportsCorruptDocuments pins the two halves of the P5
// document contract: a document that exists but does not decode is
// classified (a *state.CorruptDocumentError naming the row, never
// "never stored"), the lenient reader stays quiet, and ReadStateStrict
// is what turns the same failure into a warning an operator can act on.
func TestReadStateReportsCorruptDocuments(t *testing.T) {
	recorder := logcapture.Install(t)
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseDB() }()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	// A stored document the reader cannot decode into the shape its
	// owner expects (plans is a map, this is a JSON string).
	if err := store.WriteState(id, DocumentPlans, "not an object"); err != nil {
		t.Fatal(err)
	}
	// Opening and migrating the store logs on its own; count from here.
	logged := len(recorder.Records())

	var doc map[string]any
	err = store.ReadState(id, DocumentPlans, &doc)
	var corrupt *state.CorruptDocumentError
	if !errors.As(err, &corrupt) {
		t.Fatalf("ReadState on a corrupt document = %v, want *state.CorruptDocumentError", err)
	}
	if corrupt.ConversationID != id || corrupt.Name != DocumentPlans {
		t.Fatalf("corrupt error names %s/%s, want %s/%s",
			corrupt.ConversationID, corrupt.Name, id, DocumentPlans)
	}
	if !errors.Is(err, state.ErrCorruptDocument) {
		t.Fatalf("errors.Is(err, ErrCorruptDocument) = false (%v)", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatal("a corrupt document must not read as a missing one")
	}
	// The lenient reader decides nothing: a title read runs for every
	// conversation in the sidebar, so it reports the failure to its
	// caller without logging it.
	if len(recorder.Records()) != logged {
		t.Fatalf("ReadState logged %v, want no record", recorder.Bodies())
	}

	// The strict reader reports the same failure with the row named.
	if err := store.ReadStateStrict(id, DocumentPlans, &doc); !errors.Is(err, state.ErrCorruptDocument) {
		t.Fatalf("ReadStateStrict = %v, want ErrCorruptDocument", err)
	}
	records := recorder.Records()
	if len(records) != logged+1 {
		t.Fatalf("ReadStateStrict records = %d (%v), want %d",
			len(records), recorder.Bodies(), logged+1)
	}
	last := records[len(records)-1]
	if got := logcapture.Attribute(last, "conversation.id"); got != id {
		t.Errorf("record conversation.id = %q, want %q", got, id)
	}
	if got := logcapture.Attribute(last, "document"); got != DocumentPlans {
		t.Errorf("record document = %q, want %q", got, DocumentPlans)
	}

	// Neither reader calls a missing document a problem.
	if err := store.ReadStateStrict(id, DocumentTitle, &doc); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadStateStrict missing = %v, want os.ErrNotExist", err)
	}
	// A call the store refuses is not a document problem either: the
	// caller is already holding the error.
	if err := store.ReadStateStrict("not-a-session", DocumentTitle, &doc); err == nil {
		t.Fatal("ReadStateStrict accepted an invalid conversation id")
	}
	if len(recorder.Records()) != logged+1 {
		t.Fatalf("missing document or invalid id logged %v, want no new record",
			recorder.Bodies())
	}
}
