package core

// The content-root watcher: the desktop's half of the development loop.
//
// capabilities/apps owns the polling — what a content root is, when a
// change has settled, and whether a change is the frontend bundle's or
// the runtime's (see watch.go there). This file owns the two things that
// need the desktop: what each kind of change does to the pool, and how
// the page hears about it.
//
// It is a desktop feature on purpose. A headless run (`opencraft run`,
// execd's children) has no one editing content roots under it, and a
// watcher there would poll trees nothing is going to touch.

import (
	"context"
	"fmt"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
)

// appWatch is the running watcher's lifecycle: the cancel that stops the
// poll loop and the channel closed when it has stopped for good.
type appWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartAppWatch begins watching the registry's content roots. It is a
// no-op when this launch has no application root, or when a watcher is
// already running.
func (c *Core) StartAppWatch() {
	store := c.Runtime.Apps()
	if store == nil {
		return
	}
	c.mu.Lock()
	if c.appWatchRun != nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(c.Shell.Context())
	run := &appWatch{cancel: cancel, done: make(chan struct{})}
	c.appWatchRun = run
	c.mu.Unlock()
	watcher := apps.NewWatcher(store, apps.WatchOptions{
		Report: func(ch apps.WatchChange) {
			c.applyAppContentChange(ctx, ch)
		},
	})
	// The baseline is taken here rather than on the watcher's goroutine:
	// "the watcher is watching" is then true when this returns, so an
	// edit made the moment startup finishes is a change rather than the
	// tree the watcher happened to arrive on.
	watcher.Baseline(ctx)
	go func() {
		defer close(run.done)
		watcher.Run(ctx)
	}()
}

// StopAppWatch stops the watcher and waits for the poll in flight. The
// wait is what keeps a reload triggered by an edit — which assembles a
// Host — from running beside the runtime teardown that closes it.
func (c *Core) StopAppWatch() {
	c.mu.Lock()
	run := c.appWatchRun
	c.appWatchRun = nil
	c.mu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	<-run.done
}

// applyAppContentChange is what one settled change under a content root
// does, and the only place that decides between the two kinds.
func (c *Core) applyAppContentChange(ctx context.Context, ch apps.WatchChange) {
	if ch.Assets {
		// Only the frontend bundle changed: the runtime reads none of
		// those files, so its Host stands and the page's job is to load
		// the module again. The event says which half changed — the
		// page's store is the one place that knows a bundle reload is
		// not a registry change.
		c.Shell.Emit(EventAppChanged, AppChangedEvent{ID: ch.ID, Assets: true})
		return
	}
	// The runtime's inputs changed: retire the Host so the next turn
	// assembles the document the edit wrote, and tell the page either
	// way — it has a bundle to reload and a card to re-read.
	if err := c.ReloadApp(ctx, ch.ID); err != nil {
		// Nobody asked for this reload; an author's edit did. So the
		// failure is a log line here and the application's own answer
		// everywhere else: the next turn that needs this Host reports
		// the same refusal, which is where a broken layer eventually
		// has to be read anyway.
		telemetry.WarnErr(ctx, fmt.Sprintf(
			"desktop: reloading edited application %q failed", ch.ID), err)
	}
	c.Shell.Emit(EventAppChanged, AppChangedEvent{ID: ch.ID})
}
