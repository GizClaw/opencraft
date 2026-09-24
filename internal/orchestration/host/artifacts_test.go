package host

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// TestArtifactOwnerSkipsWritesWithoutATurn pins the guards against work
// the archive cannot own: the warning a delegated subagent used to
// produce on every file it wrote (the session store only owns "s-"
// conversations, so a "ctx-" run buffers nothing instead of reporting a
// rejected store call), and a run the engine never identified, whose
// files have no turn to belong to.
func TestArtifactOwnerSkipsWritesWithoutATurn(t *testing.T) {
	ctx := func(id, runID string) context.Context {
		return runContext(context.Background(), id, runID)
	}
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		want    string
		wantRun string
	}{
		{
			"session",
			ctx("s-6dae2e9441f8fe52", "run-1"),
			"s-6dae2e9441f8fe52", "run-1",
		},
		{"delegated subagent", ctx("ctx-4abfc8f0357c6e5d51edbc0b", "run-1"), "", ""},
		{"unidentified run", ctx("s-6dae2e9441f8fe52", ""), "", ""},
		{"no run info", context.Background(), "", ""},
		{"empty id", ctx("", "run-1"), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, gotRun := artifactOwner(tc.ctx)
			if got != tc.want || gotRun != tc.wantRun {
				t.Fatalf("owner = (%q, %q), want (%q, %q)",
					got, gotRun, tc.want, tc.wantRun)
			}
		})
	}
}

// runContext stamps the engine run info a write observer sees.
func runContext(
	parent context.Context, conversationID, runID string,
) context.Context {
	return agent.WithRunInfo(parent, agent.RunInfo{
		Identity: agent.Identity{ConversationID: conversationID, RunID: runID},
	})
}

// TestDelegationNoteDoesNotOwnTheRunningRunsFiles pins the ownership
// rule for turn artifacts: a file belongs to the turn of the run that
// wrote it. A note the app appends while that run is still going (a
// delegation result reflowed into a live conversation) must not carry
// the running run's already-buffered files onto its own row — the
// transcript would show the file under the note live and under the
// turn that wrote it after a reload.
func TestDelegationNoteDoesNotOwnTheRunningRunsFiles(t *testing.T) {
	h, _ := newReflowHost(t)
	ctx := context.Background()
	conversationID, err := h.SessionsStore().Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	const parentRun = "run-parent"
	h.onArtifactWrite(
		runContext(ctx, conversationID, parentRun), "docs/report.md", []byte("hello"))

	// The note lands while the parent run is still writing files.
	result := reflowResult(conversationID)
	h.reflowDelegation(ctx, result)
	note, err := h.SessionsStore().TurnByRunID(ctx, conversationID, result.Key())
	if err != nil {
		t.Fatalf("note turn: %v", err)
	}
	if len(note.Artifacts) != 0 {
		t.Fatalf("note artifacts = %+v, want the files to stay with the run that wrote them",
			note.Artifacts)
	}

	// The parent's own turn owns them, whether or not it is archived
	// after the note.
	if err := h.SessionsStore().AppendTurnWithRunID(
		ctx, conversationID, parentRun,
		[]message.Message{message.NewTextMessage(message.RoleUser, "ask the researcher")},
	); err != nil {
		t.Fatalf("append parent turn: %v", err)
	}
	parent, err := h.SessionsStore().TurnByRunID(ctx, conversationID, parentRun)
	if err != nil {
		t.Fatalf("parent turn: %v", err)
	}
	if len(parent.Artifacts) != 1 || parent.Artifacts[0].Path != "docs/report.md" {
		t.Fatalf("parent artifacts = %+v, want the file it wrote", parent.Artifacts)
	}
}
