package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// The development loop's second speed: an application's document can be
// swapped in place, so an author who edits a layer, a graph or a script
// keeps the Host — its conversations, its engine sessions, the turn that
// is running right now — and only the document changes. The alternative
// (retire the Host and reassemble) is what the pool does when the swap
// cannot serve the edit; the two tests below pin both halves.

// contentRoot is the installed content root of the fixture application:
// the tree an author edits under the running runtime.
func (f *appFixture) contentRoot() string {
	return filepath.Join(f.dataDir, "apps", "hello", "content")
}

// TestAppHostReloadsItsDocumentInPlace is the app platform's hot path:
// with a conversation already on the Host, an edit to the script its
// graph runs is served by the same Host, and the next turn in the same
// conversation produces the edited file. Nothing of the runtime was
// restarted — the Host pointer, the session store and the conversation
// are the ones the first turn used.
func TestAppHostReloadsItsDocumentInPlace(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hello from the app"})
	f := newAppFixture(t, provider, "")
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

	// One turn on the document as installed: it writes hello.txt.
	first, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := first.Wait(ctx); err != nil {
		t.Fatalf("wait first run: %v", err)
	}
	work := h.WorkDir()
	if _, err := os.ReadFile(filepath.Join(work, "hello.txt")); err != nil {
		t.Fatalf("the installed script did not write hello.txt: %v", err)
	}

	// The author edits the script the graph runs — the case the watcher
	// reports as a runtime change (watch.go classifies anything outside
	// the ui bundle that way).
	edited := `fs.write("edited.txt", "written after the edit\n");` + "\n"
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "scripts", "write.js"),
		[]byte(edited), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("in-place document reload: %v", err)
	}

	// Same Host, not closing, still serving.
	if got := f.mgr.Current(host.AppTarget("hello")); got != h {
		t.Fatal("the in-place reload handed back a different Host")
	}
	if h.IsClosing() {
		t.Fatal("the in-place reload retired the Host")
	}

	// The next turn writes what the edit wrote — in the conversation the
	// first turn used, which is what "without restarting the session"
	// has to mean: same conversation id, same archived history, new
	// document.
	second, err := h.StartRun(ctx, host.RunOptions{
		ContextID: first.ContextID(),
		Message:   message.NewTextMessage(message.RoleUser, "again"),
	})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	if _, err := second.Wait(ctx); err != nil {
		t.Fatalf("wait second run: %v", err)
	}
	if second.ContextID() != first.ContextID() {
		t.Fatalf("conversation changed across the reload: %q -> %q",
			first.ContextID(), second.ContextID())
	}
	data, err := os.ReadFile(filepath.Join(work, "edited.txt"))
	if err != nil {
		t.Fatalf("the edited script did not run: %v", err)
	}
	if strings.TrimSpace(string(data)) != "written after the edit" {
		t.Errorf("edited.txt = %q", data)
	}
	turn, err := h.Sessions().TurnByRunID(ctx, second.ContextID(), second.RunID())
	if err != nil {
		t.Fatalf("turn by run %s: %v", second.RunID(), err)
	}
	if !turnHasArtifact(turn.Artifacts, "edited.txt") {
		t.Errorf("turn artifacts = %+v, want edited.txt", turn.Artifacts)
	}
}

// TestAppHostKeepsServingWhenTheEditCannotBeSwapped is the other half of
// the same decision: an edit that does not describe a document the host
// can serve fails the reload, and the failure leaves the running
// generation exactly where it was. The caller (the watcher) reports it
// and falls back; the Host is not left half-swapped.
func TestAppHostKeepsServingWhenTheEditCannotBeSwapped(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hello from the app"})
	f := newAppFixture(t, provider, "")
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

	// The graph points at a file that is not there: a typo an author
	// makes, and the reason the reload must fail rather than serve a
	// half-read document.
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "layer.yaml"),
		[]byte("version: v1\nagents:\n  app:\n    engine:\n      settings:\n        graph: { file: missing.yaml }\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	err = h.ReloadDocument(ctx)
	if err == nil {
		t.Fatal("the reload accepted a document naming a graph that does not exist")
	}
	if !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("reload error = %v, want it to name the file the author has to fix", err)
	}

	// The generation the reload refused is still the one serving: a turn
	// still runs the graph it was assembled from.
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start run after the refused reload: %v", err)
	}
	if res, err := run.Wait(ctx); err != nil || res == nil || res.Status != "completed" {
		t.Fatalf("turn after the refused reload = %+v / %v, want completed", res, err)
	}
	if _, err := os.ReadFile(filepath.Join(h.WorkDir(), "hello.txt")); err != nil {
		t.Errorf("the assembled graph did not run after a refused reload: %v", err)
	}
}

// TestAppHostReloadMovesTheManifestsIdentity pins the half of a reload
// that is not the document: `agent` and `defaults` live in the manifest,
// so an author editing app.yaml changes values the serving Host keeps.
// The next turn on the same Host, in a fresh conversation, has to run on
// the model the manifest now names.
func TestAppHostReloadMovesTheManifestsIdentity(t *testing.T) {
	const (
		firstModel  = "openai-1/fake-model"
		secondModel = "openai-1/fake-model-thinks"
	)
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider,
		"defaults:\n  model: "+firstModel+"\n",
		config.Model{Name: "fake-model"},
		config.Model{
			Name: "fake-model-thinks",
			Capabilities: model.ModelCapabilities{
				Reasoning: model.ReasoningCapability{Kind: model.ReasoningToggle},
			},
		},
	)
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
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := first.Wait(ctx); err != nil {
		t.Fatalf("wait first run: %v", err)
	}
	if got := requestModel(t, provider, 0); got != "fake-model" {
		t.Fatalf("first turn ran on %q, want the manifest's first default", got)
	}

	// The author edits the manifest's defaults — the same file the
	// registry reads when it lists the application.
	manifest := `app: v1
id: hello
name: Hello
version: 0.1.0
minHostVersion: 0.1.0
agent: app
layers:
  - layer.yaml
defaults:
  model: ` + secondModel + "\n"
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "app.yaml"), []byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.ReloadDocument(ctx); err != nil {
		t.Fatalf("in-place document reload: %v", err)
	}

	second, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "again"),
	})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	if _, err := second.Wait(ctx); err != nil {
		t.Fatalf("wait second run: %v", err)
	}
	if got := requestModel(t, provider, 1); got != "fake-model-thinks" {
		t.Errorf("turn after the manifest edit ran on %q, want the edited default", got)
	}
	if got, err := h.Sessions().Model(ctx, second.ContextID()); err != nil || got != secondModel {
		t.Errorf("session model = %q (%v), want the edited default %q", got, err, secondModel)
	}
}

// TestAppHostRefusedReloadLeavesTheManifestsIdentityAlone is the
// ordering half of the identity commit: the manifest is read before
// anything is swapped and committed after the swap has landed, so a
// reload refused in between cannot leave the Host serving one
// generation under another manifest's entry agent or defaults.
//
// Where the refusal comes from is what makes the test say anything. A
// layer the preflight refuses is refused while the document loads —
// before the manifest is read at all — and a Host that never read the
// new identity has nothing it could commit. The edit here is therefore
// a manifest that moves the default model, plus a user layer that
// stopped declaring the inference wiring (the file a settings save
// leaves behind once the last provider is removed), which the reload
// refuses at the router check: one step past the read.
func TestAppHostRefusedReloadLeavesTheManifestsIdentityAlone(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider,
		"defaults:\n  model: openai-1/fake-model\n",
		config.Model{Name: "fake-model"},
		config.Model{
			Name: "fake-model-thinks",
			Capabilities: model.ModelCapabilities{
				Reasoning: model.ReasoningCapability{Kind: model.ReasoningToggle},
			},
		},
	)
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
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := first.Wait(ctx); err != nil {
		t.Fatalf("wait first run: %v", err)
	}
	if got := requestModel(t, provider, 0); got != "fake-model" {
		t.Fatalf("first turn ran on %q, want the manifest's default", got)
	}

	// The edit: a new default model, and a user layer without the
	// inference wiring.
	manifest := `app: v1
id: hello
name: Hello
version: 0.1.0
minHostVersion: 0.1.0
agent: app
layers:
  - layer.yaml
defaults:
  model: openai-1/fake-model-thinks
`
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "app.yaml"), []byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	dropInferenceWiring(t, f.configDir)
	err = h.ReloadDocument(ctx)
	if err == nil {
		t.Fatal("the reload served a document with no configured router")
	}
	if !strings.Contains(err.Error(), "router") {
		t.Fatalf("reload error = %v, want the router the layer stopped declaring", err)
	}

	// The identity the failed swap carried was not committed: the next
	// turn runs the model the Host was serving with, not the one the
	// manifest it could not serve named.
	second, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "again"),
	})
	if err != nil {
		t.Fatalf("start run after the refused reload: %v", err)
	}
	if _, err := second.Wait(ctx); err != nil {
		t.Fatalf("wait run after the refused reload: %v", err)
	}
	if got := requestModel(t, provider, 1); got != "fake-model" {
		t.Errorf("turn after the refused reload ran on %q, want the serving default %q",
			got, "fake-model")
	}
}

// dropInferenceWiring rewrites the user's own layer as the fixture
// seeds it and nothing else: the file a settings save leaves behind
// after the last provider is removed, which is a document no router can
// be built from.
func dropInferenceWiring(t *testing.T, configDir string) {
	t.Helper()
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(config.UserLayerFile(configDir), seed, 0o600); err != nil {
		t.Fatalf("write user layer: %v", err)
	}
}

// policyBrokenLayer is the fixture layer with one reserved key added: an
// edit an author can make, that decodes, that loads, and that the
// preflight refuses. It is the class of edit an in-place swap could
// otherwise serve without anyone checking it — the files it reads are
// the files the author edits.
//
// The kind is one an application layer *may* declare, so the refusal is
// the reserved key itself and nothing else: a layer allowed to shadow
// `events` with an engine of its own is a layer that rewires the
// deployment the contract layer built.
func policyBrokenLayer() string {
	return `version: v1
agents:
  app:
    card:
      name: Hello
      description: the host fixture application
    engine:
      settings:
        graph: { file: graph.yaml }
resources:
  events:
    kind: agent.Engine
`
}

// editLayerInto writes the policy-broken layer over the fixture's own.
func editLayerInto(t *testing.T, f *appFixture) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(f.contentRoot(), "layer.yaml"),
		[]byte(policyBrokenLayer()), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// TestAppHostReloadRefusesAnEditThePolicyRefuses pins what the swap does
// with a document that loads and that the platform does not serve: the
// reload fails and the generation serving stays exactly as it was. The
// caller decides what a failed swap means (the desktop retires the Host
// so the refusal reaches the author); the Host's own contract is that a
// reload either lands whole or does not happen.
func TestAppHostReloadRefusesAnEditThePolicyRefuses(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider, "")
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

	editLayerInto(t, f)
	err = h.ReloadDocument(ctx)
	if err == nil {
		t.Fatal("the reload served a layer declaring a resource the host provides")
	}
	// The sentence, not just the key: an edit that cannot be *built*
	// would also fail here, and this test is about the edit that would
	// build — a second engine under the contract's own resource key.
	for _, want := range []string{
		"layer.yaml: resources.events",
		`the host provides "events" in the contract layer`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("reload error = %v, want it to contain %q", err, want)
		}
	}

	// The refused swap left the generation alone: the Host is the same
	// one, and a turn still runs the graph it assembled from.
	if got := f.mgr.Current(host.AppTarget("hello")); got != h {
		t.Fatal("a refused reload replaced the Host")
	}
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start run after the refused reload: %v", err)
	}
	if res, err := run.Wait(ctx); err != nil || res == nil || res.Status != "completed" {
		t.Fatalf("turn after the refused reload = %+v / %v, want completed", res, err)
	}
	if _, err := os.ReadFile(filepath.Join(h.WorkDir(), "hello.txt")); err != nil {
		t.Errorf("the assembled graph did not run after a refused reload: %v", err)
	}
}

// TestAppAssemblyRefusesAnEditThePolicyRefuses is the other end of the
// same decision: an edit the swap refuses is an edit no later assembly
// may serve either. The next generation — the one the desktop builds
// after it retires the Host a refused swap left behind — is refused with
// the same reason, so the policy cannot be outlived by a rebuild.
func TestAppAssemblyRefusesAnEditThePolicyRefuses(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppFixture(t, provider, "")
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonAppTurn)

	editLayerInto(t, f)
	h, err := f.mgr.Acquire(ctx, host.AppTarget("hello"), interact.Auto{}, nil)
	if err == nil {
		h.Close()
		t.Fatal("assembly served a layer declaring a resource the host provides")
	}
	for _, want := range []string{
		"layer.yaml: resources.events",
		`the host provides "events" in the contract layer`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("assembly error = %v, want it to contain %q", err, want)
		}
	}
}
