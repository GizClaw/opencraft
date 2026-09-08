package host_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"

	ocsagents "github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestRuntimeInPlaceReloadGenerationSemantics is the M0 verification
// experiment from docs/backend-runtime-reload-plan.md: it applies a
// document-only reload through the production entry point
// (Host.ReloadDocument, which wraps flowcraft Runtime.Reload) while a
// turn is in flight and pins down which host-level resources must
// follow the new generation.
//
// Observable change: the user inference layer is rewritten to point at
// a second fake provider endpoint. The in-flight turn must finish on
// the old generation (provider A), the next turn must run on the new
// generation (provider B), and:
//   - sessions.Store stays the same shared object across generations;
//   - artifacts / agentlifecycle / hooks resource instances are new
//     per generation (so host bindings set once at assembly are stale
//     unless rebound);
//   - an external runtime.Attach subscription (the mechanism Broker
//     uses) still observes the new generation's rebuild event.
func TestRuntimeInPlaceReloadGenerationSemantics(t *testing.T) {
	providerA := fakeprovider.New(t, fakeprovider.Reply{Text: "done-a"})
	gate := providerA.HoldNext()
	providerB := fakeprovider.New(t, fakeprovider.Reply{Text: "done-b"})

	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, providerA.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	rt := h.Controller().Runtime()
	if rt == nil {
		t.Fatal("runtime is nil")
	}
	resource := func(name string) any {
		t.Helper()
		value, ok := rt.Resource(name)
		if !ok {
			t.Fatalf("runtime resource %q missing", name)
		}
		return value
	}

	// Resource identities before the reload.
	sessionsBefore := resource("sessions")
	artifactsBefore := resource("artifacts")
	agentsBefore := resource("agentlifecycle")
	hooksBefore := resource("hooks")
	storeBefore := h.Sessions()
	if sessionsBefore == nil || artifactsBefore == nil ||
		agentsBefore == nil || hooksBefore == nil {
		t.Fatalf("prereq resources missing: sessions=%v artifacts=%v agents=%v hooks=%v",
			sessionsBefore, artifactsBefore, agentsBefore, hooksBefore)
	}

	// Attach an external rebuild observer before reload. Broker uses
	// the same rt.Attach mechanism, so this doubles as the broker
	// cross-generation check.
	rebuilds := make(chan uint64, 4)
	detach, err := rt.Attach(
		ctx,
		event.Pattern(runtimecore.SubjectRuntimeRebuildCompleted()),
		event.SinkFunc(func(_ context.Context, env event.Envelope) error {
			var ev runtimecore.RuntimeRebuildEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			rebuilds <- ev.GenerationID
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("attach rebuild observer: %v", err)
	}
	defer detach()

	// Start the in-flight turn and wait until it is blocked on the
	// fake provider (old generation).
	run1, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "run before reload"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run before reload: %v", err)
	}
	select {
	case <-gate.Ready():
	case <-time.After(15 * time.Second):
		t.Fatal("provider A request did not start")
	}

	// Rewrite the user inference layer to point at provider B and
	// apply the document in place through the production entry point.
	writeFakeConfig(t, configDir, providerB.URL())
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("in-place reload: %v", err)
	}

	// The external attachment must observe the new generation's
	// rebuild-completed event (router fans out to live generations).
	select {
	case gen := <-rebuilds:
		if gen != 2 {
			t.Fatalf("observed rebuild generation = %d, want 2", gen)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("external attach did not observe the new generation rebuild")
	}

	// Sessions store must stay the same shared object.
	sessionsAfter := resource("sessions")
	if sessionsBefore != sessionsAfter {
		t.Fatal("sessions.Store instance changed across in-place reload")
	}
	if storeBefore != h.Sessions() {
		t.Fatal("host sessions store changed across in-place reload")
	}

	// Artifacts / agent lifecycle / hooks are per-generation resource
	// instances; in-place reload hands out new ones, so host bindings
	// set once at assembly time need a rebind hook.
	if artifactsBefore == resource("artifacts") {
		t.Fatal("artifacts resource instance did not change across reload (rebind unnecessary?)")
	}
	if agentsBefore == resource("agentlifecycle") {
		t.Fatal("agentlifecycle resource instance did not change across reload (rebind unnecessary?)")
	}
	if hooksBefore == resource("hooks") {
		t.Fatal("hooks resource instance did not change across reload (refresh unnecessary?)")
	}

	// The Host's runtime-reload observer must rebind the agent
	// lifecycle to the new generation's instance.
	lifecycleAfter, ok := resource("agentlifecycle").(*ocsagents.Lifecycle)
	if !ok || lifecycleAfter == nil {
		t.Fatal("agentlifecycle after reload is not a lifecycle instance")
	}
	deadline := time.Now().Add(10 * time.Second)
	for h.Agents() != lifecycleAfter {
		if time.Now().After(deadline) {
			t.Fatal("host did not rebind agent lifecycle to the new generation")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Let the in-flight turn finish on the old generation.
	gate.Release()
	res1, err := run1.Wait(ctx)
	if err != nil {
		t.Fatalf("wait in-flight run: %v", err)
	}
	if res1 == nil || res1.Status != "completed" {
		t.Fatalf("in-flight run result = %+v, want completed", res1)
	}
	if got := providerA.Calls(); got != 1 {
		t.Fatalf("provider A calls = %d, want 1", got)
	}
	if got := providerB.Calls(); got != 0 {
		t.Fatalf("provider B saw calls before its generation was live: %d", got)
	}

	// The next turn must start on the new generation (provider B).
	run2, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "run after reload"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run after reload: %v", err)
	}
	res2, err := run2.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run after reload: %v", err)
	}
	if res2 == nil || res2.Status != "completed" {
		t.Fatalf("run after reload result = %+v, want completed", res2)
	}
	if got := providerB.Calls(); got != 1 {
		t.Fatalf("provider B calls = %d, want 1", got)
	}
	if got := providerA.Calls(); got != 1 {
		t.Fatalf("provider A calls = %d, want 1 (old generation must not serve new turns)", got)
	}
}

// waitAgentLifecycleRebound waits until the Host's reload observer has
// bound the new generation's agentlifecycle instance.
func waitAgentLifecycleRebound(
	t *testing.T,
	h *host.Host,
	before *ocsagents.Lifecycle,
) *ocsagents.Lifecycle {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		value, ok := h.Controller().Runtime().Resource("agentlifecycle")
		lc, ok2 := value.(*ocsagents.Lifecycle)
		if ok && ok2 && lc != nil && lc != before {
			return lc
		}
		if time.Now().After(deadline) {
			t.Fatal("host did not rebind agent lifecycle after runtime reload")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHostArtifactObservationSurvivesDocumentReload verifies the M2
// rebind contract: after an in-place document reload the Host's
// artifact observer sink is reinstalled on the new generation's
// artifacts resource, so workspace writes during a later turn are
// still buffered into the conversation archive.
func TestHostArtifactObservationSurvivesDocumentReload(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"out.txt","content":"reload artifacts\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	before := h.Agents()
	if before == nil {
		t.Fatal("agent lifecycle missing before reload")
	}

	// Apply the same document in place (an idle settings save) and
	// wait for the Host's generation-follow hook to run.
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("document reload: %v", err)
	}
	waitAgentLifecycleRebound(t, h, before)

	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "write out.txt"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("run result = %+v, want completed", res)
	}
	data, err := os.ReadFile(filepath.Join(workDir, "out.txt"))
	if err != nil {
		t.Fatalf("write_file output missing: %v", err)
	}
	if string(data) != "reload artifacts\n" {
		t.Fatalf("out.txt = %q", data)
	}

	// The archived turn must carry the artifact: it only lands there
	// if the artifact observer sink was rebound after the reload.
	raw, ok, err := h.Sessions().State().ArchiveTurnArtifacts(
		ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("read archived artifacts: %v", err)
	}
	if !ok {
		t.Fatal("archived turn has no artifact record")
	}
	if !bytes.Contains(raw, []byte("out.txt")) {
		t.Fatalf("archived artifacts %s do not mention out.txt", raw)
	}
}

// TestSameConversationBargeInDuringReloadStaysSerial verifies that a
// second message on the same conversation while an in-place reload is
// in flight follows flowcraft's single-active-turn semantics: the new
// Start waits for the in-flight turn instead of running in parallel,
// then serves on the new generation.
func TestSameConversationBargeInDuringReloadStaysSerial(t *testing.T) {
	providerA := fakeprovider.New(t, fakeprovider.Reply{Text: "done-a"})
	gate := providerA.HoldNext()
	providerB := fakeprovider.New(t, fakeprovider.Reply{Text: "done-b"})

	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, providerA.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run1, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "first message"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	conv := run1.ContextID()
	select {
	case <-gate.Ready():
	case <-time.After(15 * time.Second):
		t.Fatal("provider A request did not start")
	}

	// Reload in place while the first turn is still blocked.
	writeFakeConfig(t, configDir, providerB.URL())
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("document reload: %v", err)
	}

	type startResult struct {
		run *host.Run
		err error
	}
	started := make(chan startResult, 1)
	go func() {
		r, err := h.StartRun(ctx, host.RunOptions{
			ContextID:     conv,
			Message:       message.NewTextMessage(message.RoleUser, "second message"),
			SkipAutoTitle: true,
		})
		started <- startResult{run: r, err: err}
	}()

	// While run1 is still in flight, the same-conversation Start must
	// not return: flowcraft interrupts and waits for finalize.
	select {
	case sr := <-started:
		if sr.err != nil {
			t.Fatalf("second run start failed: %v", sr.err)
		}
		t.Fatal("second same-conversation run started in parallel with the first")
	case <-time.After(300 * time.Millisecond):
	}
	if runs := activeRunsForConversation(h, conv); runs != 1 {
		t.Fatalf("conversation %s has %d active runs, want 1", conv, runs)
	}

	gate.Release()
	res1, err := run1.Wait(ctx)
	if err != nil {
		t.Fatalf("wait first run: %v", err)
	}
	if res1 == nil {
		t.Fatal("first run has no result")
	}

	sr := <-started
	if sr.err != nil {
		t.Fatalf("second run start failed after first finalized: %v", sr.err)
	}
	res2, err := sr.run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait second run: %v", err)
	}
	if res2 == nil || res2.Status != "completed" {
		t.Fatalf("second run result = %+v, want completed", res2)
	}
	if got := providerB.Calls(); got != 1 {
		t.Fatalf("provider B calls = %d, want 1", got)
	}
	if got := providerA.Calls(); got != 1 {
		t.Fatalf("provider A calls = %d, want 1", got)
	}
}

func activeRunsForConversation(h *host.Host, conv string) int {
	count := 0
	for _, view := range h.ActiveRuns() {
		if view.ConversationID == conv {
			count++
		}
	}
	return count
}
