package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// An application may run more than one agent: the manifest names its
// entry, and any further agent it lists is one a caller can pick by name
// (RunOptions.AgentID). The application's own frontend is the caller that
// does — a package that plays roles drives one agent per role — so the
// name has to reach the engine session, not be decoration on the way.

// writeSecondAgent turns the installed single-agent fixture into a
// two-agent application: the manifest lists a judge, and a layer
// declares it with a graph of its own. It is written over the installed
// content root, which is also what an author editing a package does —
// and the preflight runs again over the result at assembly.
func writeSecondAgent(t *testing.T, f *appFixture) {
	t.Helper()
	root := f.contentRoot()
	manifest := `app: v1
id: hello
name: Hello
version: 0.1.0
minHostVersion: 0.1.0
agent: app
agents:
  - judge
layers:
  - layer.yaml
`
	files := map[string]string{
		"app.yaml": manifest,
		"layer.yaml": `version: v1
agents:
  app:
    card:
      name: Hello
      description: the host fixture application
    engine:
      settings:
        graph: { file: graph.yaml }
  judge:
    card:
      name: Judge
      description: the second agent
    engine:
      kind: agent.Engine
      impl: graph
      deps:
        inference: infer
        router: router
        workspace: ws
        script_runtime: js
      settings:
        graph: { file: judge.yaml }
    commit:
      - type: opencraft.commit
        deps:
          memory: mem
          sessions: sessions
`,
		"judge.yaml": `name: judge
entry: verdict
nodes:
  - id: verdict
    type: script
    config:
      runtime: js
      source: { file: scripts/judge.js }
  - id: llm
    type: inference
    config:
      stream: true
      model_hint: ${board:model:}
edges:
  - { from: verdict, to: llm }
  - { from: llm, to: __end__ }
`,
		"scripts/judge.js": `fs.write("verdict.txt", "judged\n");
`,
	}
	for rel, data := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAppHostRunsTheAgentATurnNames pins the whole path of a named
// agent: the manifest lists it, the preflight accepts it, the turn runs
// its graph, and the conversation stays the one the caller named — an
// agent is a choice of who answers, not a separate conversation.
func TestAppHostRunsTheAgentATurnNames(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider, "")
	writeSecondAgent(t, f)
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonAppTurn)

	h, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("close application host: %v", err)
		}
	}()

	first, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "go first"),
	})
	if err != nil {
		t.Fatalf("start the entry agent's turn: %v", err)
	}
	if _, err := first.Wait(ctx); err != nil {
		t.Fatalf("wait the entry agent's turn: %v", err)
	}
	if got := first.AgentID(); got != "app" {
		t.Errorf("the entry agent's run reports agent %q, want app", got)
	}
	work := h.WorkDir()
	if _, err := os.ReadFile(filepath.Join(work, "hello.txt")); err != nil {
		t.Fatalf("the entry agent's graph did not run: %v", err)
	}

	second, err := h.StartRun(ctx, host.RunOptions{
		ContextID: first.ContextID(),
		AgentID:   "judge",
		Message:   message.NewTextMessage(message.RoleUser, "who wins?"),
	})
	if err != nil {
		t.Fatalf("start the judge's turn in the same conversation: %v", err)
	}
	if _, err := second.Wait(ctx); err != nil {
		t.Fatalf("wait the judge's turn: %v", err)
	}
	if second.ContextID() != first.ContextID() {
		t.Fatalf("the named agent answered in another conversation: %q -> %q",
			first.ContextID(), second.ContextID())
	}
	if got := second.AgentID(); got != "judge" {
		t.Errorf("the named agent's run reports agent %q, want judge", got)
	}
	if _, err := os.ReadFile(filepath.Join(work, "verdict.txt")); err != nil {
		t.Fatalf("the named agent's own graph did not run: %v", err)
	}
	// The named agent commits to the same transcript the entry agent
	// writes: one conversation, one history, whichever agent answered.
	// (Its hooks are its own — the layer above wires the judge to the
	// contract's own commit hook — because an agent the platform did not
	// declare is an agent the platform does not decide the pipeline of.)
	judged, err := h.Sessions().TurnByRunID(ctx, second.ContextID(), second.RunID())
	if err != nil {
		t.Fatalf("turn by run %s: %v", second.RunID(), err)
	}
	if !turnHasText(judged.Messages, "who wins?") {
		t.Errorf("the named agent's turn is not in the conversation: %+v", judged.Messages)
	}
	// The entry agent's graph is not the one that ran: its script writes
	// hello.txt on every turn, and the judge's turn left it alone.
	before, err := os.Stat(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	second2, err := h.StartRun(ctx, host.RunOptions{
		ContextID: first.ContextID(),
		AgentID:   "judge",
		Message:   message.NewTextMessage(message.RoleUser, "and again"),
	})
	if err != nil {
		t.Fatalf("start the judge's second turn: %v", err)
	}
	if _, err := second2.Wait(ctx); err != nil {
		t.Fatalf("wait the judge's second turn: %v", err)
	}
	after, err := os.Stat(filepath.Join(work, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a turn naming the judge ran the entry agent's graph")
	}
}

// TestAppHostRefusesAnAgentItDoesNotKnow: a name the application does
// not declare is a bundle asking for an agent the package never
// described, and the refusal names the ones that exist — the entry first
// — so the answer says what to write instead.
func TestAppHostRefusesAnAgentItDoesNotKnow(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider, "")
	writeSecondAgent(t, f)
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonAppTurn)

	h, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("close application host: %v", err)
		}
	}()

	_, err = h.StartRun(ctx, host.RunOptions{
		AgentID: "nobody",
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err == nil {
		t.Fatal("a turn naming an agent the application does not declare was accepted")
	}
	if !strings.Contains(err.Error(), `agent "nobody" is not one of this host's agents`) ||
		!strings.Contains(err.Error(), "app, judge") {
		t.Errorf("refusal = %v, want it to name the deployed agents", err)
	}
	// Nothing was started: a refused name is not a turn that ran the
	// entry agent instead.
	if runs := h.ActiveRuns(); len(runs) != 0 {
		t.Errorf("the refused turn left %d runs behind", len(runs))
	}
}

// TestAppHostReloadMovesTheAgentList: the agents a caller may name are
// manifest half of the Host, so an in-place reload moves them with the
// document. An author who drops an agent from the package stops being
// able to run it, and the entry keeps working.
func TestAppHostReloadMovesTheAgentList(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider, "")
	writeSecondAgent(t, f)
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonAppTurn)

	h, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("close application host: %v", err)
		}
	}()

	run, err := h.StartRun(ctx, host.RunOptions{
		AgentID: "judge",
		Message: message.NewTextMessage(message.RoleUser, "who wins?"),
	})
	if err != nil {
		t.Fatalf("start the judge's turn: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait the judge's turn: %v", err)
	}

	// The author drops the agent: the manifest stops listing it and the
	// layer stops declaring it. Both halves have to move for the reload
	// to be servable, which is itself the rule the preflight states.
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "app.yaml"),
		[]byte(`app: v1
id: hello
name: Hello
version: 0.1.0
minHostVersion: 0.1.0
agent: app
layers:
  - layer.yaml
`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "layer.yaml"),
		[]byte(`version: v1
agents:
  app:
    card:
      name: Hello
      description: the host fixture application
    engine:
      settings:
        graph: { file: graph.yaml }
`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("in-place document reload: %v", err)
	}

	if _, err := h.StartRun(ctx, host.RunOptions{
		AgentID: "judge",
		Message: message.NewTextMessage(message.RoleUser, "and now?"),
	}); err == nil {
		t.Fatal("the reload kept an agent the manifest no longer names")
	}
	entry, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("the entry agent did not survive the edit: %v", err)
	}
	if _, err := entry.Wait(ctx); err != nil {
		t.Fatalf("wait the entry agent's turn: %v", err)
	}
}

// TestWorkspaceHostHasOneAgent: the same rule seen from the other side.
// A workspace's Host runs the assistant and nothing else, and its agent
// list is one name — the list is what a refusal reads back to a caller
// that named something else, so a workspace answering "assistant,
// assistant" would be the platform describing itself wrongly.
func TestWorkspaceHostHasOneAgent(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	h, _ := acquireHostFixture(t, provider)
	ctx := context.Background()

	// Naming the assistant is naming the agent the Host runs: a caller
	// that spells out the default is not asking for something else.
	run, err := h.StartRun(ctx, host.RunOptions{
		AgentID: "assistant",
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start the assistant's turn by name: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait the assistant's turn: %v", err)
	}
	if got := run.AgentID(); got != "assistant" {
		t.Errorf("the assistant's run reports agent %q, want assistant", got)
	}

	_, err = h.StartRun(ctx, host.RunOptions{
		AgentID: "judge",
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err == nil {
		t.Fatal("a workspace host accepted a name that is not its agent")
	}
	if want := `agents (assistant)`; !strings.Contains(err.Error(), want) {
		t.Errorf("refusal = %v, want it to read %q", err, want)
	}
}

// TestAppHostDeletesConversationsOfAMultiAgentPackage: deleting closes
// every engine session the conversation had — one per agent that ran in
// it — and closing is tolerant of the ones that did not. An application
// whose manifest lists agents the user never picked still has to be
// deletable, and the entry-only case is the common one.
//
// (What the per-agent loop buys is the engine's own bookkeeping and
// memory: an application may name a listed agent again, and the id it
// would name is retired, so a session left behind is unreachable either
// way. This test pins what a caller can see — deleted means deleted,
// whichever agents ran.)
func TestAppHostDeletesConversationsOfAMultiAgentPackage(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider, "")
	writeSecondAgent(t, f)
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonAppTurn)

	h, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("close application host: %v", err)
		}
	}()

	// One conversation both agents answered in.
	entry, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "go first"),
	})
	if err != nil {
		t.Fatalf("start the entry agent's turn: %v", err)
	}
	if _, err := entry.Wait(ctx); err != nil {
		t.Fatalf("wait the entry agent's turn: %v", err)
	}
	judge, err := h.StartRun(ctx, host.RunOptions{
		ContextID: entry.ContextID(),
		AgentID:   "judge",
		Message:   message.NewTextMessage(message.RoleUser, "who wins?"),
	})
	if err != nil {
		t.Fatalf("start the judge's turn: %v", err)
	}
	if _, err := judge.Wait(ctx); err != nil {
		t.Fatalf("wait the judge's turn: %v", err)
	}
	if err := h.DeleteConversation(ctx, entry.ContextID()); err != nil {
		t.Fatalf("delete a conversation two agents answered in: %v", err)
	}
	if h.Sessions().Exists(entry.ContextID()) {
		t.Fatalf("conversation %q survived its deletion", entry.ContextID())
	}

	// And one only the entry agent touched: the judge's session does not
	// exist, which the deletion has to tolerate.
	only, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "just me"),
	})
	if err != nil {
		t.Fatalf("start the entry agent's second turn: %v", err)
	}
	if _, err := only.Wait(ctx); err != nil {
		t.Fatalf("wait the entry agent's second turn: %v", err)
	}
	if err := h.DeleteConversation(ctx, only.ContextID()); err != nil {
		t.Fatalf("delete a conversation only the entry agent ran in: %v", err)
	}
	if h.Sessions().Exists(only.ContextID()) {
		t.Fatalf("conversation %q survived its deletion", only.ContextID())
	}
}
