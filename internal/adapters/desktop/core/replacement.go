package core

import (
	"context"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// The adapter's half of the pool's deferred replacement policy. The pool
// owns when a retired assembly is replaced (a Host with live runs drains
// first and its successor is assembled afterwards); this file answers
// the two questions it asks of the composition root, per scope: does
// this target still want a runtime, and who has to hear that one landed.
//
// Both scopes arrive here because both are pooled by target, and the
// answers differ in kind: a workspace is wanted while the window is on
// it (the user moved away, so nothing is rebuilt behind their back),
// while an application is wanted while the registry says it is
// installed and enabled (a disable mid-drain is a decision not to come
// back). Neither scope answers for the other's targets: a workspace
// reload must not resurrect a disabled application, and an application
// reload must not touch the window's workspace.

// replacementWanted is what the pool asks before assembling a successor.
func (c *Core) replacementWanted(t host.Target) bool {
	if t.Kind == host.TargetApp {
		return c.appWanted(t.ID)
	}
	// The comparison is the pool's own (host.SameTarget), so a target
	// this accepts is also the one the pool keys as the window's
	// workspace: a second, hand-rolled comparison could accept a
	// spelling the pool keeps a separate Host for. An open window with
	// no workspace refuses every target — an unnamed target matches
	// nothing — so it is never rebuilt either.
	return host.SameTarget(t, host.WorkspaceTarget(c.ActiveWorkDir()))
}

// appWanted reports whether one application still wants a runtime: it is
// installed and enabled as of now. It reads the registry rather than a
// snapshot taken when the drain began, because the answer decides
// whether a Host is assembled: a user who disabled or uninstalled the
// application while its last generation drained has said no, and a
// launch without a registry (no app home) serves no application at all.
func (c *Core) appWanted(id string) bool {
	store := c.Runtime.Apps()
	if store == nil {
		return false
	}
	app, err := store.Get(id)
	if err != nil {
		// Not installed (or unreadable): nothing to assemble for. The
		// page reads the same registry and shows the refusal.
		return false
	}
	return app.Enabled
}

// replacementInstalled is what the pool calls after a successor was
// assembled and pooled.
func (c *Core) replacementInstalled(t host.Target) {
	if t.Kind == host.TargetApp {
		// The application page's card listens here: an application's
		// runtime came back after a drain. It is not the window's
		// workspace, so this must not refresh the workspace's own view
		// (ready would make the UI re-render the wrong deployment).
		c.Shell.Emit(EventAppStatus, AppStatusEvent{ID: t.ID, Serving: true})
		return
	}
	c.EmitReady()
}

// ReloadApp invalidates one application's Host and brings the
// application back from what its content root now holds: the next
// generation serves whatever the layers say today. It is the
// application-scoped counterpart of the rebuilds above — the update,
// rollback and development-loop path, which names the one application it
// changed instead of retiring every one of them — and it never touches
// the workspace scope.
//
// A Host with work in flight cannot be replaced until it drains (a
// second one would serve the same conversations concurrently), so that
// swap is deferred to the pool exactly as a workspace's is, and the
// successor is announced as app_status when it lands. A disabled or
// uninstalled application gets no successor at all: its state is the
// page's answer, not a runtime.
func (c *Core) ReloadApp(ctx context.Context, id string) error {
	if err := c.Runtime.ReloadApps(ctx, id); err != nil {
		return err
	}
	if !c.appWanted(id) {
		// Disabled or uninstalled: the invalidation was the whole job.
		return nil
	}
	target := host.AppTarget(id)
	h, err := c.Runtime.EnsureHost(
		host.WithAssemblyReason(ctx, host.ReasonAppReload), target,
	)
	if err != nil {
		// The application is enabled but cannot be served as it stands
		// (a layer edited into an invalid document, a graph that does
		// not assemble). The caller is the page that asked for the
		// reload and has a card to put this on, so it comes back as the
		// reload's own failure rather than as a log line.
		return err
	}
	if h.IsStale() {
		// Work is still running on the generation this reload retired.
		// Arming is once per target, so a storm inside one drain asks
		// for one replacement.
		c.Runtime.ScheduleReplacement(ctx, target)
		return nil
	}
	c.Shell.Emit(EventAppStatus, AppStatusEvent{ID: id, Serving: true})
	return nil
}

// ReloadAppDocument applies an edit under one application's content root
// to the Host already serving it, in place: the same Host, the same
// conversations, the same engine sessions, one new document. It is the
// development loop's path — an author edits a layer, a graph or a script
// while the application is running, and the next turn reads what they
// wrote instead of what the process assembled first.
//
// It is not the install/update path: a package that was swapped on disk
// is reassembled (ReloadApp), because a new generation of the content
// deserves the checks assembly runs. Here nothing was installed — one
// file changed under a package that already assembled once — so the
// cheap swap is the right one, and its failure falls back to the
// expensive path rather than surfacing on a card: an edit the swap
// cannot serve — a sessions implementation change, a document that does
// not load, a layer the preflight refuses — is one the Host should not
// be left running on either, and retiring it is what puts the refusal
// where the author reads it (the next assembly's record, and the card).
//
// An application with no Host has nothing to swap — the next turn
// assembles the file the edit wrote — and a retiring Host is being
// replaced by an assembly that reads the same file, so neither pays for
// a reload here.
func (c *Core) ReloadAppDocument(ctx context.Context, id string) error {
	h := c.Runtime.HostFor(host.AppTarget(id))
	if h == nil || h.IsStale() {
		return nil
	}
	err := h.ReloadDocument(ctx)
	if err == nil {
		return nil
	}
	telemetry.WarnErr(ctx,
		"desktop: in-place application document reload unavailable; rebuilding",
		err,
		otellog.String("app_id", id),
		otellog.String("reason", string(host.AssemblyReasonFrom(ctx))))
	return c.ReloadApp(ctx, id)
}
