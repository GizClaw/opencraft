package bindings

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// An update is the one registry write that replaces content an
// application may already be serving, which is why it has two halves and
// these tests are about both: the swap the registry does, and the runtime
// that has to move onto the new bytes without cutting a turn short — plus
// the way back, because an update is the only write that leaves one.

// bundleVersion is the version writeAppBundle writes into the fixture's
// manifest.
const bundleVersion = "0.1.0"

// versionedBundle is the fixture package at another version, optionally
// with its graph replaced. A package the preflight accepts is one whose
// references all resolve, so it is the bytes a reference points at — a
// graph that is not a document — that make a version assemble-fail.
func versionedBundle(t *testing.T, version, graph string) string {
	t.Helper()
	dir := writeAppBundle(t)
	manifest := readBundleFile(t, dir, apps.ManifestFile)
	writeBundleFile(t, dir, apps.ManifestFile,
		strings.Replace(manifest, "version: "+bundleVersion, "version: "+version, 1))
	writeBundleFile(t, dir, "version.txt", version+"\n")
	if graph != "" {
		writeBundleFile(t, dir, "graph.yaml", graph)
	}
	return dir
}

// brokenGraph is valid YAML the preflight cannot follow and the engine
// cannot load: the scan gives up on a document it cannot convert, so the
// package is accepted and the assembly is what refuses it.
const brokenGraph = "::: not a graph\n"

func readBundleFile(t *testing.T, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeBundleFile(t *testing.T, dir, rel, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// installAndEnable is the state every update starts from: one installed,
// enabled application with a runtime of its own. The package it installs
// is the fixture stamped with its version, so a test can read the live
// version back out of the content root either side of an update.
func installAndEnable(t *testing.T, f *appBindingFixture) string {
	t.Helper()
	if _, err := f.binding.Install(
		versionedBundle(t, bundleVersion, ""), AppInstallOptions{},
	); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	return filepath.Join(f.dataDir, "apps", "hello", "content")
}

// TestAppUpdateSwapsTheContentAndMovesTheRuntime is the whole path in one
// test: the new version is live, the runtime is a new generation on it,
// the application's state is where it was, and the page heard about it.
func TestAppUpdateSwapsTheContentAndMovesTheRuntime(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	content := installAndEnable(t, f)
	before := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if before == nil {
		t.Fatal("an enabled application has no runtime")
	}
	seen := f.watch()

	sum, err := f.binding.Update("hello", versionedBundle(t, "0.2.0", ""))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if sum.Version != "0.2.0" || !sum.Enabled || !sum.CanRollback {
		t.Fatalf("summary = %+v", sum)
	}
	// The content root is the new package...
	if got := readBundleFile(t, content, "version.txt"); got != "0.2.0\n" {
		t.Fatalf("live content = %q", got)
	}
	// ...with nothing of the application's own data in it, and the state
	// root still holding what the install already wrote.
	if _, err := os.Stat(filepath.Join(content, "sessions")); err == nil {
		t.Fatal("the content root grew a state directory")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "apps", "hello", "sessions", "session.db")); err != nil {
		t.Fatalf("the state root did not survive the update: %v", err)
	}
	// The runtime moved: the old generation is retired, the new one is
	// assembled on the new bytes.
	after := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if after == nil || after == before {
		t.Fatalf("runtime after the update = %v, want a new generation", after)
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Serving || status.Retiring {
		t.Fatalf("status after the update = %+v", status)
	}
	names := eventNames(seen())
	for _, want := range []string{core.EventAppChanged, core.EventAppStatus} {
		if !slices.Contains(names, want) {
			t.Fatalf("events = %v, want %s among them", names, want)
		}
	}
}

// TestAppUpdateThatCannotBeServedIsReportedAndRolledBack pins the message
// a failed assembly after a successful swap has to carry — which half
// happened — and the way back: the rollback the update left behind
// restores service.
func TestAppUpdateThatCannotBeServedIsReportedAndRolledBack(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	content := installAndEnable(t, f)

	_, err := f.binding.Update("hello", versionedBundle(t, "0.2.0", brokenGraph))
	if err == nil || !strings.Contains(err.Error(), "cannot be served") {
		t.Fatalf("update to a version that cannot assemble = %v", err)
	}
	// The content is the new version all the same: the registry accepted
	// it, so the card shows it and offers the snapshot back.
	if got := readBundleFile(t, content, "version.txt"); got != "0.2.0\n" {
		t.Fatalf("content after the failed serve = %q", got)
	}
	list, err := f.binding.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Version != "0.2.0" || !list[0].CanRollback {
		t.Fatalf("card after the failed serve = %+v", list)
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Serving || status.Retiring {
		t.Fatalf("a version that cannot assemble is serving: %+v", status)
	}

	sum, err := f.binding.Rollback("hello")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if sum.Version != bundleVersion || sum.CanRollback {
		t.Fatalf("rollback summary = %+v", sum)
	}
	if got := readBundleFile(t, content, "version.txt"); got != bundleVersion+"\n" {
		t.Fatalf("rolled-back content = %q", got)
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Serving || status.Retiring {
		t.Fatalf("the rolled-back version is not serving: %+v", status)
	}
}

// TestAppUpdateOfADisabledApplicationSkipsTheRuntime: nothing wanted a
// runtime before the update, so nothing is assembled after it — and the
// application still enables on the content that landed.
func TestAppUpdateOfADisabledApplicationSkipsTheRuntime(t *testing.T) {
	f := newAppBinding(t, fakeprovider.New(t, fakeprovider.Reply{Text: "hi"}))
	content := installAndEnable(t, f)
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	sum, err := f.binding.Update("hello", versionedBundle(t, "0.2.0", ""))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if sum.Enabled {
		t.Fatalf("the update enabled a disabled application: %+v", sum)
	}
	if h := f.core.Runtime.HostFor(host.AppTarget("hello")); h != nil {
		t.Fatalf("a disabled application got a runtime: %v", h)
	}
	if got := readBundleFile(t, content, "version.txt"); got != "0.2.0\n" {
		t.Fatalf("content = %q", got)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable after the update: %v", err)
	}
}

// TestAppUpdateDefersUntilTheRunningTurnEnds is the promise that makes an
// update safe to run on a working application: the turn keeps the
// generation it started on, ends normally, and the successor is assembled
// on the new content once the drain is over.
func TestAppUpdateDefersUntilTheRunningTurnEnds(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	installAndEnable(t, f)
	before := f.core.Runtime.HostFor(host.AppTarget("hello"))
	seen := f.watch()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gate := provider.HoldNext()
	t.Cleanup(gate.Release)
	run, err := before.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	select {
	case <-gate.Ready():
	case <-ctx.Done():
		t.Fatal("the turn never reached the provider")
	}

	if _, err := f.binding.Update("hello", versionedBundle(t, "0.2.0", "")); err != nil {
		t.Fatalf("update during a turn: %v", err)
	}
	if f.core.Runtime.HostFor(host.AppTarget("hello")) != before {
		t.Fatal("the update replaced the Host while a turn was still running on it")
	}

	gate.Release()
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	after := waitForAppHost(t, f, before)
	if after.IsClosing() {
		t.Fatal("the successor was still retiring when it landed")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "apps", "hello", "content", "version.txt")); err != nil {
		t.Fatalf("the successor is not on the new content: %v", err)
	}
	names := eventNames(seen())
	if !slices.Contains(names, core.EventAppStatus) {
		t.Fatalf("the page never heard the replacement land: %v", names)
	}
}

// TestAppUpdateAndRollbackRefusals is the guard rail: an id nothing is
// installed under, a rollback with nothing behind it, and a built-in
// application each say what is wrong instead of half-doing something.
func TestAppUpdateAndRollbackRefusals(t *testing.T) {
	f := newAppBinding(t, nil)
	installAndEnable(t, f)
	src := versionedBundle(t, "0.2.0", "")

	if _, err := f.binding.Update("gone", src); err == nil {
		t.Fatal("an update of an application that is not installed succeeded")
	}
	if _, err := f.binding.Rollback("gone"); err == nil {
		t.Fatal("a rollback of an application that is not installed succeeded")
	}
	_, err := f.binding.Rollback("hello")
	if err == nil || !strings.Contains(err.Error(), "no update") {
		t.Fatalf("rollback with nothing behind it = %v", err)
	}
	if _, err := f.binding.Update("hello", src); err != nil {
		t.Fatalf("update: %v", err)
	}
	// The same package twice: the version rule, not the id rule.
	_, err = f.binding.Update("hello", versionedBundle(t, "0.1.0", ""))
	if err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("update to an older version = %v", err)
	}
}
