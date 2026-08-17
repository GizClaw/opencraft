package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/sessions"
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

func newTestStore(t *testing.T) *sessions.Store {
	t.Helper()
	store, err := sessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	return store
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

	if err := store.SetMode("s1", sessions.ModeYOLO); err != nil {
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
	if err := store.SetMode("s1", sessions.ModeYOLO); err != nil {
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
