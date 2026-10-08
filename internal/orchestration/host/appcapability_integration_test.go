package host_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// The capability fragments an application opts into are how it reaches a
// surface v1 does not hand out: the host owns the fragment, the manifest
// names it, and the generated wiring points every agent the application
// runs at what the fragments built. These two tests are the two halves
// of that sentence for the one fragment whose acceptance is a turn —
// with the name in the manifest the host's own file tools answer the
// model, inside the application's private workspace; without it the same
// package does not assemble.

// toolAppManifest is the fixture manifest: the smallest application plus
// whatever capability line the caller adds, so the two tests differ in
// one line and nothing else.
func toolAppManifest(manifestExtra string) string {
	return `app: v1
id: hello
name: Hello
version: 0.1.0
minHostVersion: 0.1.0
agent: app
layers:
  - layer.yaml
` + manifestExtra
}

// writeToolAppPackage writes the fixture of these tests: the smallest
// application whose graph runs a tool — an inference node offering the
// whole catalog, a tool node executing what the model asked for, and the
// edge that loops back for the answer.
func writeToolAppPackage(t *testing.T, manifestExtra string) string {
	t.Helper()
	return writePackage(t, map[string]string{
		apps.ManifestFile: toolAppManifest(manifestExtra),
		"layer.yaml": `version: v1
agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`,
		"graph.yaml": `name: hello
entry: llm
nodes:
  - id: llm
    type: inference
    config:
      # The whole tool catalog, the way the assistant's graph offers it:
      # what the model sees is the graph's decision, and the fragments
      # decide what is in the catalog.
      all_tools: true
      tool_pending_key: tool_pending
      stream: true
  - id: tools
    type: tool
    config:
      results_key: tool_results
edges:
  - { from: llm, to: tools, condition: "tool_pending == true" }
  - { from: llm, to: __end__ }
  - { from: tools, to: llm }
`,
	})
}

// TestAppHostRunsTheToolsItsManifestEnabled is the fragment's
// acceptance: a package that names "tools" gets the host's file tools,
// the model's call reaches them, and the file it wrote is the
// application's own — in its private workspace, attributed to the turn
// like any other write (the fragment's workspace carries the same
// artifact observer the contract layer's does).
func TestAppHostRunsTheToolsItsManifestEnabled(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name: "write_file",
			Arguments: `{"file_path": "from-tool.txt", ` +
				`"content": "written by a tool\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	f := newAppFixtureFrom(t, provider,
		writeToolAppPackage(t, "capabilities:\n  - tools\n"))
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
		Message: message.NewTextMessage(message.RoleUser, "write me a file"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("result = %+v, want completed", res)
	}

	// The tool wrote into the application's private workspace — the
	// fragment's workspace resource resolves to the contract layer's
	// `ws`, so a tool's write lands where the application's own script
	// writes land, and nowhere else.
	path := filepath.Join(f.appStateRoot(), "workspace", "from-tool.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the tool's file is not in the application's workspace: %v", err)
	}
	if string(data) != "written by a tool\n" {
		t.Errorf("from-tool.txt = %q", data)
	}

	// The model saw the result: the round after the call carries a tool
	// message answering the call it made, which is what the loop back
	// through the tool node is for.
	messages, err := provider.MessagesForCalls()
	if err != nil {
		t.Fatalf("read the provider's requests: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("the provider was called %d times, want the tool round and the answer", len(messages))
	}
	if answer := toolResultFor(messages[1], "from-tool.txt"); answer == "" {
		t.Errorf("the second request does not carry the tool result: %+v", messages[1])
	}

	// And the write is the turn's: the artifact record the application's
	// page reads its file strip from names it.
	turn, err := h.Sessions().TurnByRunID(ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("turn by run %s: %v", run.RunID(), err)
	}
	if !turnHasArtifact(turn.Artifacts, "from-tool.txt") {
		t.Errorf("turn artifacts = %+v, want from-tool.txt", turn.Artifacts)
	}
}

// offeredTools lists the tool names one request offered the model, the
// way the provider received them: what the model can choose from is the
// catalog the deployment's containers built, so a fragment that arrived
// is visible here and a fragment that did not is visible as its
// absence.
func offeredTools(t *testing.T, provider *fakeprovider.Server, call int) []string {
	t.Helper()
	requests, err := provider.RequestField("tools")
	if err != nil {
		t.Fatalf("read the requests: %v", err)
	}
	if call >= len(requests) {
		t.Fatalf("the provider saw %d requests, want one for call %d",
			len(requests), call)
	}
	entries, _ := requests[call].([]any)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := obj["function"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := fn["name"].(string); ok {
			out = append(out, name)
		}
	}
	return out
}

// TestAppHostReloadAddsAFragment: an author who edits an installed
// package's manifest is making the same edit as one who wrote the line
// before the install. The manifest's half of the mechanism is read
// twice — at assembly and at every reload — and a wiring built from the
// first read would leave the author's edit unreachable until the
// application was restarted, which is exactly what the in-place swap
// exists to avoid.
func TestAppHostReloadAddsAFragment(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixtureFrom(t, provider,
		writeToolAppPackage(t, "capabilities:\n  - tools\n"))
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

	before, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := before.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	// The file tools are the fragment that was named, and the web tools
	// are the fragment that was not.
	if names := offeredTools(t, provider, 0); !slices.Contains(names, "write_file") {
		t.Fatalf("the first turn was offered %v, want the file tools", names)
	} else if slices.Contains(names, "web_fetch") {
		t.Fatalf("the first turn was offered the web tools: %v", names)
	}

	// The edit, in the installed content root — the file the registry
	// re-reads on every reload.
	installed := filepath.Join(f.contentRoot(), apps.ManifestFile)
	edited := toolAppManifest("capabilities:\n  - tools\n  - web\n")
	if err := os.WriteFile(installed, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("in-place document reload: %v", err)
	}

	after, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "and now"),
	})
	if err != nil {
		t.Fatalf("start the run after the edit: %v", err)
	}
	if _, err := after.Wait(ctx); err != nil {
		t.Fatalf("wait the run after the edit: %v", err)
	}
	names := offeredTools(t, provider, 1)
	for _, want := range []string{"write_file", "web_fetch"} {
		if !slices.Contains(names, want) {
			t.Errorf("after the edit the model was offered %v, want %s", names, want)
		}
	}
}

// toolResultFor returns the content of the tool message in one request
// that mentions needle, or "" when the request carries none. The
// provider's wire shape is the OpenAI one, so a tool result is a
// {"role": "tool"} entry whose content is the tool's text.
func toolResultFor(messages []map[string]any, needle string) string {
	for _, msg := range messages {
		if msg["role"] != "tool" {
			continue
		}
		content, _ := msg["content"].(string)
		if strings.Contains(content, needle) {
			return content
		}
	}
	return ""
}

// TestAppHostRefusesToolsWithoutTheCapability: the same package without
// the manifest's line does not run. An application's graph is written
// against what it was given, so a tool node (or a catalog the model may
// choose from) is a dependency on the host's tool assembly — the node
// type is registered either way, the document has to declare where its
// dispatcher comes from — and the refusal names that dependency.
//
// The install itself is accepted: a layer that runs a tool node is not
// illegal, it is unbound, and what an application may reach is decided
// by the manifest. So the sentence lands at assembly, which is where an
// application's document is checked for real: the card and the
// diagnostics panel are what a user sees, and the graph engine's own
// words are what the author fixes.
func TestAppHostRefusesToolsWithoutTheCapability(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "unused"})
	f := newAppFixtureFrom(t, provider, writeToolAppPackage(t, ""))
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonAppTurn)

	h, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if err == nil {
		_ = h.Close()
		t.Fatal("a package that uses the host's tools without naming the capability assembled")
	}
	want := `node type tool requires dep "tools"`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to read %q", err, want)
	}
}
