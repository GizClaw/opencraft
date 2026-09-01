package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/resource"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/tools/files"
)

// sessionCtx wraps ctx with the RunInfo flowcraft injects during graph
// execution so HostSandbox/HostWorkspace can resolve the session.
func sessionCtx(id string) context.Context {
	return agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", ConversationID: id},
	})
}

type recordRunner struct {
	mu   sync.Mutex
	used int
}

func (r *recordRunner) Close() error { return nil }

func (r *recordRunner) Capabilities() coresandbox.Capabilities {
	return coresandbox.Capabilities{}
}

func (r *recordRunner) Start(
	context.Context, coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	r.mu.Lock()
	r.used++
	r.mu.Unlock()
	return nil, nil
}

func (r *recordRunner) List(context.Context) ([]coresandbox.SessionInfo, error) {
	return nil, nil
}

func (r *recordRunner) Terminate(context.Context, string) error { return nil }

func (r *recordRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used
}

// captureOptsRunner records the ExecOptions each confined Start
// receives so tests can assert the per-call write policy injection.
type captureOptsRunner struct {
	got []coresandbox.ExecOptions
}

func (r *captureOptsRunner) Close() error { return nil }

func (r *captureOptsRunner) Capabilities() coresandbox.Capabilities {
	return coresandbox.Capabilities{}
}

func (r *captureOptsRunner) Start(
	_ context.Context, spec coresandbox.SessionSpec,
) (coresandbox.Session, error) {
	r.got = append(r.got, spec.Opts)
	return nil, nil
}

func (r *captureOptsRunner) List(context.Context) ([]coresandbox.SessionInfo, error) {
	return nil, nil
}

func (r *captureOptsRunner) Terminate(context.Context, string) error { return nil }

func newTestStore(t *testing.T) *sessions.Store {
	t.Helper()
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestHostSandboxInjectsReadOnlyWritePolicy(t *testing.T) {
	confined := &captureOptsRunner{}
	unconfined := &recordRunner{}
	store := newTestStore(t)
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMode(context.Background(), id, sessions.ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
	}

	if _, err := hs.Start(
		sessionCtx(id), coresandbox.SessionSpec{ID: "ro"},
	); err != nil {
		t.Fatal(err)
	}
	if len(confined.got) != 1 {
		t.Fatalf("confined Start calls = %d, want 1", len(confined.got))
	}
	if confined.got[0].Write != coresandbox.WriteReadOnly {
		t.Fatalf("read-only session write policy = %v, want WriteReadOnly",
			confined.got[0].Write)
	}
	if unconfined.count() != 0 {
		t.Fatal("read-only session must not use the unconfined runner")
	}

	// Workspace mode keeps the zero-value (runner construction-time)
	// boundary, and yolo still routes to the unconfined runner.
	wsID, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hs.Start(
		sessionCtx(wsID), coresandbox.SessionSpec{ID: "ws"},
	); err != nil {
		t.Fatal(err)
	}
	if confined.got[1].Write != coresandbox.WriteWorkspace {
		t.Fatalf("workspace session write policy = %v, want WriteWorkspace",
			confined.got[1].Write)
	}
	if err := store.SetMode(context.Background(), wsID, sessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.Start(
		sessionCtx(wsID), coresandbox.SessionSpec{ID: "yolo"},
	); err != nil {
		t.Fatal(err)
	}
	if len(confined.got) != 2 {
		t.Fatal("yolo session must not reach the confined runner")
	}
	if unconfined.count() != 1 {
		t.Fatalf("yolo session unconfined Start calls = %d, want 1", unconfined.count())
	}
}

func TestHostSandboxSwitchesBySessionMode(t *testing.T) {
	confined := &recordRunner{}
	unconfined := &recordRunner{}
	store := newTestStore(t)
	hs := &HostSandbox{
		sessions:   store,
		confined:   confined,
		unconfined: unconfined,
	}

	if _, err := hs.Start(
		sessionCtx("s1"), coresandbox.SessionSpec{ID: "a"},
	); err != nil {
		t.Fatal(err)
	}
	if confined.count() != 1 || unconfined.count() != 0 {
		t.Fatalf("workspace mode: %d confined / %d unconfined starts",
			confined.count(), unconfined.count())
	}

	if err := store.SetMode(context.Background(), "s1", sessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.Start(
		sessionCtx("s1"), coresandbox.SessionSpec{ID: "b"},
	); err != nil {
		t.Fatal(err)
	}
	if confined.count() != 1 || unconfined.count() != 1 {
		t.Fatalf("yolo mode: %d confined / %d unconfined starts",
			confined.count(), unconfined.count())
	}

	// A different session stays confined.
	if _, err := hs.Start(
		sessionCtx("s2"), coresandbox.SessionSpec{ID: "c"},
	); err != nil {
		t.Fatal(err)
	}
	if confined.count() != 2 || unconfined.count() != 1 {
		t.Fatalf("other session: %d confined / %d unconfined starts",
			confined.count(), unconfined.count())
	}
}

func TestHostWorkspaceSwitchesBySessionMode(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	confined, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	hw := &HostWorkspace{
		sessions: store,
		confined: confined,
		host:     &hostWorkspace{root: root},
	}

	if _, err := hw.Read(sessionCtx("s1"), outside); err == nil {
		t.Fatal("workspace mode must reject paths outside the root")
	}
	if err := store.SetMode(context.Background(), "s1", sessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if _, err := hw.Read(sessionCtx("s1"), outside); err == nil {
		t.Fatal("yolo read of a missing host path should still error")
	}
	if err := hw.Write(sessionCtx("s1"), outside, []byte("s3cret")); err != nil {
		t.Fatalf("yolo write outside root: %v", err)
	}
	data, err := hw.Read(sessionCtx("s1"), outside)
	if err != nil || string(data) != "s3cret" {
		t.Fatalf("yolo read = %q, %v", data, err)
	}
}

func TestHostWorkspaceReadonlyRoots(t *testing.T) {
	root := t.TempDir()
	readonlyDir := t.TempDir()
	skillFile := filepath.Join(readonlyDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("skill body"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherOutside := filepath.Join(t.TempDir(), "secret.txt")
	confined, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	hw := &HostWorkspace{
		sessions: newTestStore(t),
		confined: confined,
		host:     &hostWorkspace{root: root},
		readonly: []string{readonlyDir},
	}

	// Workspace mode: read-only roots are readable...
	data, err := hw.Read(sessionCtx("s1"), skillFile)
	if err != nil || string(data) != "skill body" {
		t.Fatalf("readonly root read = %q, %v", data, err)
	}
	// ...but writes still go to the confined workspace and are rejected.
	if err := hw.Write(sessionCtx("s1"), skillFile, []byte("x")); err == nil {
		t.Fatal("write to readonly root must be rejected in workspace mode")
	}
	// Paths outside both the root and the readonly allowlist stay denied.
	if _, err := hw.Read(sessionCtx("s1"), otherOutside); err == nil {
		t.Fatal("workspace mode must reject paths outside root and readonly roots")
	}
}

// TestHostWorkspaceFactoryDerivesRootFromWorkspace verifies the
// factory takes the confined workspace from the workspace dependency
// and derives the host/YOLO resolution root from it, instead of a
// settings.root override.
func TestHostWorkspaceFactoryDerivesRootFromWorkspace(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	value, err := (HostWorkspaceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"sessions":  store,
			"workspace": ws,
		},
	})
	if err != nil {
		t.Fatalf("factory new: %v", err)
	}
	hw, ok := value.(*HostWorkspace)
	if !ok {
		t.Fatalf("factory returned %T, want *HostWorkspace", value)
	}
	if hw.root != ws.Root() {
		t.Fatalf("hostws root = %q, want ws root %q", hw.root, ws.Root())
	}

	// Workspace mode: writes outside the derived root are rejected.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := hw.Write(sessionCtx("s1"), outside, []byte("x")); err == nil {
		t.Fatal("workspace mode must reject writes outside the derived root")
	}
	// YOLO mode: host resolution still anchors on the derived root.
	if err := store.SetMode(context.Background(), "s1", sessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if err := hw.Write(sessionCtx("s1"), outside, []byte("s3cret")); err != nil {
		t.Fatalf("yolo write outside root: %v", err)
	}
	data, err := hw.Read(sessionCtx("s1"), outside)
	if err != nil || string(data) != "s3cret" {
		t.Fatalf("yolo read = %q, %v", data, err)
	}
}

// TestHostWorkspaceFactoryAcceptsMatchingLegacyRoot keeps older configs
// working: a settings.root that resolves to the same directory as the
// workspace dependency is accepted.
func TestHostWorkspaceFactoryAcceptsMatchingLegacyRoot(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := (HostWorkspaceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"root":` + strconv.Quote(root) + `}`),
		Deps: map[string]any{
			"sessions":  newTestStore(t),
			"workspace": ws,
		},
	})
	if err != nil {
		t.Fatalf("factory new with matching legacy root: %v", err)
	}
	hw, ok := value.(*HostWorkspace)
	if !ok || hw.root != ws.Root() {
		t.Fatalf("factory returned %T with root %q, want ws root %q",
			value, safeRoot(hw), ws.Root())
	}
}

// TestHostWorkspaceFactoryRejectsMismatchedRoot fails loudly when a
// legacy settings.root points at a different directory than ws, so
// confinement and readonly resolution can never drift apart.
func TestHostWorkspaceFactoryRejectsMismatchedRoot(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = (HostWorkspaceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"root":` + strconv.Quote(t.TempDir()) + `}`),
		Deps: map[string]any{
			"sessions":  newTestStore(t),
			"workspace": ws,
		},
	})
	if err == nil {
		t.Fatal("mismatched settings.root must be rejected")
	}
	if !strings.Contains(err.Error(), "does not match the workspace dependency root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func safeRoot(hw *HostWorkspace) string {
	if hw == nil {
		return ""
	}
	return hw.root
}

// TestFilesToolReadsReadonlySkillRoot verifies the full chain for the
// reported skill-reference read: the file tools pass absolute paths
// through to the workspace, whose readonly skill roots are served
// read-only in workspace mode, while everything else stays denied.
func TestFilesToolReadsReadonlySkillRoot(t *testing.T) {
	root := t.TempDir()
	skillDir := t.TempDir()
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(refDir, "deploy.md")
	if err := os.WriteFile(skillFile, []byte("skill reference body"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	confined, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	hw := &HostWorkspace{
		sessions: newTestStore(t),
		confined: confined,
		host:     &hostWorkspace{root: root},
		readonly: []string{skillDir},
	}
	ft, err := files.New(hw)
	if err != nil {
		t.Fatal(err)
	}
	var readFile, writeFile tool.Tool
	for _, tt := range ft.Tools() {
		switch tt.Definition().Name {
		case files.ReadFileName:
			readFile = tt
		case files.WriteFileName:
			writeFile = tt
		}
	}
	if readFile == nil || writeFile == nil {
		t.Fatal("files tool group missing read_file/write_file")
	}
	ctx := sessionCtx("s1")

	// Workspace mode: a skill reference under the readonly root is
	// readable through read_file with its absolute path...
	got, err := readFile.Execute(ctx, `{"file_path":"`+skillFile+`"}`)
	if err != nil {
		t.Fatalf("read_file skill reference: %v", err)
	}
	if !strings.Contains(got, "skill reference body") {
		t.Errorf("read_file result = %q, want skill body", got)
	}
	// ...paths outside both the root and the readonly allowlist stay
	// denied...
	if _, err := readFile.Execute(ctx, `{"file_path":"`+outside+`"}`); err == nil {
		t.Fatal("read_file outside workspace and readonly roots must fail")
	}
	// ...and writes never escape to readonly roots.
	if _, err := writeFile.Execute(ctx,
		`{"file_path":"`+skillFile+`","content":"x"}`); err == nil {
		t.Fatal("write_file to readonly root must fail")
	}
}
