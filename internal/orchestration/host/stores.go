// Store accessors on Manager: the user-level stores plus the
// per-workspace sessions store (leased per workspace, shared inside).

package host

import (
	"context"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	metricstore "github.com/GizClaw/opencraft/internal/capabilities/telemetry/metric"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"

	otellog "go.opentelemetry.io/otel/log"
)

// UsageStore returns the user-level usage store attached by
// OpenUserDB, or nil before the database is open.
func (m *Manager) UsageStore() *usage.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userUsage
}

// AutomationsStore returns the user-level automation store attached
// by OpenUserDB, or nil before the database is open.
func (m *Manager) AutomationsStore() *automations.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userAutomations
}

// MetricsStore returns the user-level local metric store attached by
// OpenUserDB, or nil before the database is open.
func (m *Manager) MetricsStore() *metricstore.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userMetrics
}

// MemoryStore returns the user-level long-term memory store attached by
// OpenUserDB, or nil before the database is open.
func (m *Manager) MemoryStore() *userstore.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userMemory
}

// SkillUsageStore returns the skill lifecycle store attached by
// OpenUserDB, or nil before the database is open.
func (m *Manager) SkillUsageStore() *skillusage.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userSkillUsage
}

// ReviewStore returns the review suggestion queue attached by
// OpenUserDB, or nil before the database is open.
func (m *Manager) ReviewStore() *reviewstore.Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userReview
}

// OpenSessions returns the shared Store for one workspace without
// assembling a Host. It runs the same first-open adoption and
// migration path as Host acquisition. Callers must ReleaseSessions.
func (m *Manager) OpenSessions(
	ctx context.Context, workDir string, layout config.WorkspaceLayout, window int,
) (*sessions.Store, error) {
	return m.acquireStore(ctx, workDir, layout.SessionsDir, window)
}

// ReleaseSessions drops one caller's reference to a shared Store.
func (m *Manager) ReleaseSessions(store *sessions.Store) {
	m.releaseStore(store)
}

// acquireStore opens one Store per root and reference-counts it.
// workDir supplies the v0.1.x project-local session location that is
// adopted into the new layout on first open.
func (m *Manager) acquireStore(
	ctx context.Context, workDir, root string, window int,
) (*sessions.Store, error) {
	root = filepath.Clean(root)
	m.mu.Lock()
	if ref := m.stores[root]; ref != nil {
		ref.refs++
		s := ref.store
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	// Serialize first-open adoption/schema work across Host assembly
	// and adapter-only store opens for every workspace.
	m.openMu.Lock()
	defer m.openMu.Unlock()

	m.mu.Lock()
	if ref := m.stores[root]; ref != nil {
		ref.refs++
		s := ref.store
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	if err := compat.AdoptLegacySessions(
		ctx, compat.LegacySessionsDir(workDir), root,
	); err != nil {
		return nil, err
	}

	store, err := sessions.New(root, window)
	if err != nil {
		return nil, err
	}
	if err := compat.Workspace(
		ctx, store.Database(), root, state.Importer(store.Database()),
	); err != nil {
		telemetry.WarnErr(ctx, "host: close store after workspace migration failure",
			store.CloseDB())
		return nil, err
	}
	m.mu.Lock()
	if ref := m.stores[root]; ref != nil {
		telemetry.WarnErr(ctx, "host: close duplicate session store",
			store.CloseDB())
		ref.refs++
		s := ref.store
		m.mu.Unlock()
		return s, nil
	}
	m.stores[root] = &storeRef{store: store, refs: 1}
	m.mu.Unlock()
	m.scheduleSearchBackfill(ctx, root, store)
	return store, nil
}

// scheduleSearchBackfill runs the one-time message-index backfill for a
// database written before the full-text index existed. The walk is
// proportional to the archive (minutes on a large database), so it runs
// detached from the open path and best-effort: the store is usable
// immediately, and a failure only means the next open tries again (the
// backfill records its version solely on success). It deliberately does
// not hold a pool reference: if the last caller releases the store
// while the walk runs, the walk aborts on the closed handle and the
// next open resumes it.
func (m *Manager) scheduleSearchBackfill(
	ctx context.Context, root string, store *sessions.Store,
) {
	database := store.Database()
	if database == nil {
		return
	}
	// The walk outlives the call that opened the store: keep its values
	// (identity, telemetry), drop its cancellation.
	walkCtx := context.WithoutCancel(ctx)
	go func() {
		if err := compat.BackfillSearchIndex(
			walkCtx, database, state.Importer(database),
		); err != nil {
			telemetry.WarnErr(walkCtx,
				"host: message search index backfill failed; retried on the next open",
				err, otellog.String("root", root))
		}
	}()
}

// releaseStore drops one runtime reference to a shared Store.
func (m *Manager) releaseStore(store *sessions.Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for root, ref := range m.stores {
		if ref.store != store {
			continue
		}
		ref.refs--
		if ref.refs == 0 {
			delete(m.stores, root)
			telemetry.WarnErr(context.Background(),
				"host: close released session store failed", store.CloseDB())
		}
		return
	}
}
