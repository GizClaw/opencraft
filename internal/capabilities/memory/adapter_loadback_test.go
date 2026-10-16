package memory

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

// LoadBack walks by what it read, not by what it kept. A page whose
// rows the projection all leaves out (a withheld reasoning trace has no
// prompt form) is a page that returned nothing while the transcript
// continues behind it — reading that as "the conversation start was
// reached" would silently drop the older rows from the window.
func TestLoadBackStepsOverPagesTheProjectionEmpties(t *testing.T) {
	adapter, store := newSQLiteTurnStore(t)
	ctx := context.Background()
	const conv = "s-1"

	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, "最老的一行"),
		message.NewTextMessage(message.RoleAssistant, "第二老的一行"),
	}
	// Enough unrenderable rows to fill the first backward page (n +
	// projectionBackfill) on their own.
	for i := 0; i < 40; i++ {
		msgs = append(msgs, message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ReasoningPart{Signature: "opaque"},
			}},
		})
	}
	commitTurn(t, store, conv, "turn-1", msgs)

	maxSeq, err := adapter.MaxSeq(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	back, err := adapter.LoadBack(ctx, conv, maxSeq+1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 {
		t.Fatalf("rows = %d, want the two renderable rows behind the skipped page",
			len(back))
	}
	if back[0].Seq != 0 || back[0].Content.Text() != "最老的一行" {
		t.Errorf("row 0 = seq %d %q", back[0].Seq, back[0].Content.Text())
	}
	if back[1].Seq != 1 || back[1].Content.Text() != "第二老的一行" {
		t.Errorf("row 1 = seq %d %q", back[1].Seq, back[1].Content.Text())
	}
}
