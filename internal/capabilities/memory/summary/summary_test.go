package summary

import (
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

// textRows builds a transcript fixture: user messages with seqs counting
// up from zero.
func textRows(texts ...string) []StoredMessage {
	out := make([]StoredMessage, 0, len(texts))
	for i, text := range texts {
		out = append(out, StoredMessage{
			Seq:     int64(i),
			Role:    message.RoleUser,
			Content: message.NewTextContent(text),
		})
	}
	return out
}

func TestSourceIDIsTheTranscriptCoordinate(t *testing.T) {
	if got := SourceID(0); got != "0" {
		t.Fatalf("SourceID(0) = %q, want \"0\"", got)
	}
	if got := SourceID(41); got != "41" {
		t.Fatalf("SourceID(41) = %q, want \"41\"", got)
	}
	rows := textRows("hello", "world", "again")
	node, err := BufferFold(
		Policy{MaxRawMessages: 1, PreserveRecent: 1}, "t1", rows, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("want a fold over three rows with raw 1 + preserve 1")
	}
	if len(node.SourceIDs) != 1 || node.SourceIDs[0] != SourceID(rows[0].Seq) {
		t.Fatalf("source ids = %v, want [%s]", node.SourceIDs, SourceID(rows[0].Seq))
	}
	if node.Generation() != IdentitySeqV1 || !node.CurrentGeneration() {
		t.Fatalf("node generation = %q, want %q",
			node.Generation(), IdentitySeqV1)
	}
}
