package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// writeAppPackage writes the smallest application that still exercises
// every acceptance item of this step — a manifest, one deployment
// layer, the graph it names, and the script that graph runs — into a
// throwaway directory, and returns it. The script writes one file
// through the workspace binding (the only write surface v1 gives an
// application; see the plan's appendix A), and the inference node after
// it is what makes the turn report usage.
func writeAppPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		apps.ManifestFile: `app: v1
id: hello
name: Hello
version: 0.1.0
minHostVersion: 0.1.0
agent: app
layers:
  - layer.yaml
`,
		"layer.yaml": `version: v1
agents:
  app:
    card:
      name: Hello
      description: the host fixture application
    engine:
      settings:
        graph: { file: graph.yaml }
`,
		"graph.yaml": `name: hello
entry: write
nodes:
  - id: write
    type: script
    config:
      runtime: js
      source: { file: scripts/write.js }
  - id: llm
    type: inference
    config:
      stream: true
edges:
  - { from: write, to: llm }
  - { from: llm, to: __end__ }
`,
		"scripts/write.js": `fs.write("hello.txt", "written by the app\n");
`,
	}
	for rel, data := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// usageRecord is one call the manager made into its usage recorder,
// with the attribution key it carried.
type usageRecord struct {
	workspaceID string
	sessionID   string
	usage       ocsessions.Usage
}

// appFixture is one installed application plus the host that serves it:
// the single-root launch (app home and state root are the same tree,
// which is the layout the content level exists for), one registry, one
// manager, and the fake provider the runtime talks to.
type appFixture struct {
	mgr       *host.Manager
	registry  *apps.Store
	provider  *fakeprovider.Server
	dataDir   string
	configDir string
	// usage collects every recorder call, so an assertion can be about
	// the key the manager attributed a turn to instead of about the
	// user database's aggregation.
	usage []usageRecord
}

// newAppFixture builds the launch an application runs in and installs
// the fixture package into it. The registry is wired the way a
// composition root wires it (apps.NewRegistry over the resolved launch
// paths), so the assembly reads exactly what an install wrote.
func newAppFixture(t *testing.T, provider *fakeprovider.Server) *appFixture {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	registry, err := apps.NewRegistry(dataDir, dataDir)
	if err != nil {
		t.Fatalf("app registry: %v", err)
	}
	if _, err := registry.Install(context.Background(), writeAppPackage(t)); err != nil {
		t.Fatalf("install fixture application: %v", err)
	}
	mgr := host.NewManagerAt(dataDir, configDir)
	mgr.SetAppRegistry(registry)
	t.Cleanup(mgr.CloseUserDB)

	f := &appFixture{
		mgr:       mgr,
		registry:  registry,
		provider:  provider,
		dataDir:   dataDir,
		configDir: configDir,
	}
	mgr.SetUsageRecorder(func(
		_ context.Context,
		workspaceID, sessionID string,
		usage ocsessions.Usage,
		_ time.Time,
	) error {
		f.usage = append(f.usage, usageRecord{
			workspaceID: workspaceID,
			sessionID:   sessionID,
			usage:       usage,
		})
		return nil
	})
	return f
}

// appStateRoot is the application's state root: what the layout owns
// under the launch's data dir.
func (f *appFixture) appStateRoot() string {
	return filepath.Join(f.dataDir, "apps", "hello")
}

// TestAppHostRunsATurnInItsOwnState is the P0 acceptance for the app
// host: acquire the Host of an installed application, run one turn, and
// find its four consequences where they belong — the turn in the
// application's own session database, the script's file in the
// application's private workspace (with the write attributed to that
// run), the usage attributed to the application's key, and nothing left
// behind once the conversation is deleted.
func TestAppHostRunsATurnInItsOwnState(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hello from the app"})
	f := newAppFixture(t, provider)
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

	// The Host knows what it serves, and its work dir is the
	// application's private workspace — not the content root the
	// package was installed into, and not the launch's own directory.
	wantWork := filepath.Join(f.appStateRoot(), "workspace")
	if got := h.AppID(); got != "hello" {
		t.Fatalf("AppID() = %q, want hello", got)
	}
	if got := h.WorkDir(); got != wantWork {
		t.Fatalf("WorkDir() = %q, want %q", got, wantWork)
	}

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
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

	// (1) The turn is archived in the application's own session
	// database, under a session id of the shape the store mints.
	if !ids.IsSession(run.ContextID()) {
		t.Errorf("conversation id = %q, want an %s session", run.ContextID(), ids.SessionPrefix)
	}
	if _, err := os.Stat(filepath.Join(f.appStateRoot(), "sessions", "session.db")); err != nil {
		t.Errorf("application session database missing: %v", err)
	}
	turn, err := h.Sessions().TurnByRunID(ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("turn by run %s: %v", run.RunID(), err)
	}
	if turn.Status != "completed" {
		t.Errorf("archived turn status = %q, want completed", turn.Status)
	}
	if !turnHasText(turn.Messages, "hi") {
		t.Errorf("archived turn does not carry the user message: %+v", turn.Messages)
	}

	// (2) The script's file is in the private workspace, and the write
	// is attributed to this run (the artifact pipeline the assistant
	// uses, with the application's own workspace as the root).
	data, err := os.ReadFile(filepath.Join(wantWork, "hello.txt"))
	if err != nil {
		t.Fatalf("application workspace file missing: %v", err)
	}
	if string(data) != "written by the app\n" {
		t.Errorf("hello.txt = %q", data)
	}
	if !turnHasArtifact(turn.Artifacts, "hello.txt") {
		t.Errorf("turn artifacts = %+v, want hello.txt", turn.Artifacts)
	}
	// Nothing of the runtime landed in the content root the package
	// was installed into: the layers are read there, never written.
	contentRoot := filepath.Join(f.dataDir, "apps", "hello", "content")
	if _, err := os.Stat(filepath.Join(contentRoot, "workspace")); !os.IsNotExist(err) {
		t.Errorf("content root grew a workspace: %v", err)
	}

	// (3) Usage is attributed to the application's key, not to a path
	// hash and not to the assistant's workspace.
	if len(f.usage) == 0 {
		t.Fatal("no usage was recorded for the application turn")
	}
	for _, rec := range f.usage {
		if rec.workspaceID != "app:hello" {
			t.Errorf("usage workspace = %q, want app:hello", rec.workspaceID)
		}
		if rec.sessionID != run.ContextID() {
			t.Errorf("usage session = %q, want %q", rec.sessionID, run.ContextID())
		}
		if rec.usage.TotalTokens <= 0 {
			t.Errorf("usage = %+v, want a reported token count", rec.usage)
		}
	}

	// (4) Deleting the conversation is final and leaves no residue: the
	// rows are gone, the session directory with them, and the rest of
	// the application's state is untouched.
	installMarker := filepath.Join(f.appStateRoot(), "workspace", "hello.txt")
	if err := h.DeleteConversation(ctx, run.ContextID()); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	metas, err := h.Sessions().List()
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	for _, meta := range metas {
		if meta.ID == run.ContextID() {
			t.Errorf("deleted conversation %s is still listed", meta.ID)
		}
	}
	if h.Sessions().Exists(run.ContextID()) {
		t.Errorf("deleted conversation %s still exists", run.ContextID())
	}
	if _, err := os.Stat(filepath.Join(f.appStateRoot(), "sessions", run.ContextID())); !os.IsNotExist(err) {
		t.Errorf("session directory survived the delete: %v", err)
	}
	if _, err := os.Stat(installMarker); err != nil {
		t.Errorf("delete took the application's workspace with it: %v", err)
	}

	// An application's turn never paid for a background title: the
	// provider saw the graph's one call and nothing else. Close waits
	// for the title job a workspace turn would have started, so the
	// count is settled by the time it returns.
	if err := h.Close(); err != nil {
		t.Fatalf("close application host: %v", err)
	}
	if calls := provider.Calls(); calls != 1 {
		t.Errorf("provider calls = %d, want 1 (the graph's own; an application turn adds no title call)", calls)
	}
}

// TestAppHostRefusesWhatTheRegistryDoesNotOffer pins the two refusals an
// assembly makes before it builds anything: an id nothing installed, and
// an application the user disabled. Neither is retryable — waiting for a
// replacement Host cannot make an uninstalled or disabled application
// serve a turn — and neither may fall back to a workspace runtime.
func TestAppHostRefusesWhatTheRegistryDoesNotOffer(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "unused"})
	f := newAppFixture(t, provider)
	ctx := listContext()

	if _, err := f.mgr.Acquire(ctx, host.AppTarget("not-installed"), interact.Auto{}, nil); err == nil {
		t.Fatal("acquiring an uninstalled application must fail")
	} else if !strings.Contains(err.Error(), "no such application") {
		t.Errorf("error = %v, want the registry's own refusal", err)
	}

	if err := f.registry.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable application: %v", err)
	}
	if _, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil); err == nil {
		t.Fatal("acquiring a disabled application must fail")
	} else if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %v, want the disabled refusal", err)
	}

	// A manager with no registry cannot build an application at all,
	// and says so instead of assembling a workspace for the target.
	bare := host.NewManagerAt(t.TempDir(), t.TempDir())
	_, err := bare.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if !errors.Is(err, host.ErrNoAssembly) {
		t.Fatalf("error = %v, want ErrNoAssembly for an unbuildable target kind", err)
	}
}

// TestWorkspaceTargetsKeepTheWorkspaceBuilder guards the one thing the
// application path must not change: a workspace still assembles from
// the workspace builder, with the assistant as its agent.
func TestWorkspaceTargetsKeepTheWorkspaceBuilder(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hello"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	h, err := mgr.Acquire(context.Background(),
		host.WorkspaceTarget(workDir), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire workspace host: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("close workspace host: %v", err)
		}
	}()
	if h.AppID() != "" {
		t.Errorf("AppID() = %q, want empty for a workspace", h.AppID())
	}
	if h.WorkDir() != workDir {
		t.Errorf("WorkDir() = %q, want %q", h.WorkDir(), workDir)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "apps")); !os.IsNotExist(err) {
		t.Errorf("a workspace host wrote application state: %v", err)
	}
}

// TestAppHostIsConfiguredOnTheHandoffPath: the adapter's event wiring
// (the host configurator) runs for an application's Host exactly as it
// does for a workspace's — on the path that hands the Host out, once
// per pooled Host. Without it an application's artifact and
// session_updated events would never leave the process, and the
// failure is invisible here: everything else about the Host works.
func TestAppHostIsConfiguredOnTheHandoffPath(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "unused"})
	f := newAppFixture(t, provider)
	var configured []host.Target
	f.mgr.SetHostConfigurator(func(h *host.Host) {
		configured = append(configured, h.Target())
	})

	ctx := context.Background()
	target := host.AppTarget("hello")
	first, err := f.mgr.Acquire(ctx, target, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host: %v", err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Errorf("close application host: %v", err)
		}
	}()
	second, err := f.mgr.Acquire(ctx, target, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host again: %v", err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("close application host again: %v", err)
		}
	}()

	if first != second {
		t.Error("a second acquire for one application built a second host")
	}
	if len(configured) != 1 {
		t.Fatalf("configurator ran %d times, want 1: %+v", len(configured), configured)
	}
	if configured[0] != target {
		t.Errorf("configurator saw target %s, want %s", configured[0], target)
	}
}

// TestAppHostAdoptsNoLegacyStore: the project-local `.opencraft/sessions`
// tree is a v0.1.x *workspace's* predecessor, relocated into the new
// store on first open. An application never had one, and its state root
// must not pick one up from wherever the process happens to run — the
// legacy location is derived from the work dir, and an application has
// none.
func TestAppHostAdoptsNoLegacyStore(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "unused"})
	f := newAppFixture(t, provider)

	// A decoy legacy tree exactly where an unguarded implementation
	// would look for one: the process's working directory.
	cwd := t.TempDir()
	legacyFile := filepath.Join(cwd, ".opencraft", "sessions", "s-legacy", "turn.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	h, err := f.mgr.Acquire(host.WithAssemblyReason(context.Background(), host.ReasonAppTurn),
		host.AppTarget("hello"), interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire application host: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			t.Errorf("close application host: %v", err)
		}
	}()

	if _, err := os.Stat(legacyFile); err != nil {
		t.Errorf("the application assembly moved a legacy store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.appStateRoot(), "sessions", "s-legacy")); !os.IsNotExist(err) {
		t.Errorf("a legacy conversation was adopted into the application's state root: %v", err)
	}
}

// listContext is the context a registry read runs under.
func listContext() context.Context { return context.Background() }

// turnHasText reports whether any archived message carries text.
func turnHasText(messages []message.Message, text string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content.Text(), text) {
			return true
		}
	}
	return false
}

// turnHasArtifact reports whether the turn's artifact list names path.
func turnHasArtifact(artifacts []ocsessions.Artifact, path string) bool {
	for _, a := range artifacts {
		if a.Path == path {
			return true
		}
	}
	return false
}
