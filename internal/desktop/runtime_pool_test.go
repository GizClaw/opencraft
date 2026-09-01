package desktop

import (
	"path/filepath"
	"sync"
	"testing"

	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/undo"
)

func newPoolTestApp() *App {
	return &App{
		mu:              sync.Mutex{},
		backgroundHosts: make(map[string]*backgroundHost),
	}
}

func fakeBackgroundHost(a *App, wd string) *backgroundHost {
	return &backgroundHost{
		app:             a,
		workDir:         wd,
		turns:           make(map[string]*coresession.Turn),
		runConvs:        make(map[string]string),
		runUsage:        make(map[string]ocsessions.Usage),
		preTurnSnap:     make(map[string][]undo.FileState),
		preTurnManifest: make(map[string]map[string]fileStat),
		rollouts:        make(map[string]*rollout.Recorder),
		rolloutBufs:     make(map[string]*rolloutBuffer),
	}
}

func TestBackgroundHostForMissingWorkspace(t *testing.T) {
	a := newPoolTestApp()
	if _, err := a.backgroundHostFor(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing workspace must fail")
	}
	if len(a.backgroundHosts) != 0 {
		t.Fatalf("pool must stay empty, got %d hosts", len(a.backgroundHosts))
	}
}

func TestInvalidateBackgroundHostsClosesIdleAndStalesBusy(t *testing.T) {
	a := newPoolTestApp()
	wd := t.TempDir()
	idle := fakeBackgroundHost(a, wd)
	busy := fakeBackgroundHost(a, filepath.Join(wd, "busy"))
	busy.turns["r-1"] = &coresession.Turn{}
	a.backgroundHosts[idle.workDir] = idle
	a.backgroundHosts[busy.workDir] = busy

	a.invalidateBackgroundHosts()

	if !idle.closed {
		t.Fatal("idle host must be closed")
	}
	if _, ok := a.backgroundHosts[idle.workDir]; ok {
		t.Fatal("idle host must leave the pool")
	}
	if !busy.stale {
		t.Fatal("busy host must be marked stale")
	}
	if _, ok := a.backgroundHosts[busy.workDir]; !ok {
		t.Fatal("busy host must stay in the pool until its run ends")
	}
}

func TestReapBackgroundHostOnlyAfterLastTurn(t *testing.T) {
	a := newPoolTestApp()
	wd := t.TempDir()
	h := fakeBackgroundHost(a, wd)
	h.turns["r-1"] = &coresession.Turn{}
	a.backgroundHosts[wd] = h
	h.stale = true

	a.reapBackgroundHost(h)
	if h.closed {
		t.Fatal("host with an active turn must not close")
	}
	if _, ok := a.backgroundHosts[wd]; !ok {
		t.Fatal("host with an active turn must stay in the pool")
	}

	delete(h.turns, "r-1")
	a.reapBackgroundHost(h)
	if !h.closed {
		t.Fatal("stale host with no turns must close")
	}
	if _, ok := a.backgroundHosts[wd]; ok {
		t.Fatal("closed host must leave the pool")
	}
}

func TestCloseBackgroundHostsAndIdempotentClose(t *testing.T) {
	a := newPoolTestApp()
	wd := t.TempDir()
	h := fakeBackgroundHost(a, wd)
	a.backgroundHosts[wd] = h

	a.closeBackgroundHosts()
	if !h.closed {
		t.Fatal("closeBackgroundHosts must close every host")
	}
	if len(a.backgroundHosts) != 0 {
		t.Fatalf("pool must be empty, got %d hosts", len(a.backgroundHosts))
	}
	h.close()
	if !h.closed {
		t.Fatal("close must be idempotent")
	}
}

func TestBackgroundHostForReusesLiveHost(t *testing.T) {
	a := newPoolTestApp()
	wd := t.TempDir()
	h := fakeBackgroundHost(a, wd)
	a.backgroundHosts[wd] = h

	got, err := a.backgroundHostFor(wd)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatal("live host must be reused without reassembly")
	}
}
