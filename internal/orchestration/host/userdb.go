// User-level database: opening/migrating user.db and the one-time
// bookkeeping steps that ride on the open.

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	metricstore "github.com/GizClaw/opencraft/internal/capabilities/telemetry/metric"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/assembly"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"

	otellog "go.opentelemetry.io/otel/log"
)

// OpenUserDB opens the user-level database (user.db under the manager
// data root) once, applies user migrations and attaches the usage and
// automations stores. It is idempotent and safe to call from several
// adapters sharing the manager. The default usage recorder starts
// persisting as soon as the usage store is attached.
func (m *Manager) OpenUserDB(ctx context.Context) error {
	m.mu.Lock()
	if m.userDB != nil {
		m.mu.Unlock()
		return nil
	}
	dataDir := m.dataDir
	userDir := m.userDir
	m.mu.Unlock()
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return fmt.Errorf("host: resolve user data dir: %w", err)
		}
	}

	// Serialize first open so two adapters cannot migrate and attach
	// the same database concurrently.
	m.openMu.Lock()
	defer m.openMu.Unlock()

	m.mu.Lock()
	if m.userDB != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	handle, err := db.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		return fmt.Errorf("host: open user db: %w", err)
	}
	if err := compat.User(ctx, handle); err != nil {
		telemetry.WarnErr(ctx, "host: close user db after migration failure",
			handle.Close())
		return fmt.Errorf("host: migrate user db: %w", err)
	}
	usageStore, err := usage.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx, "host: close user db after usage attach failure",
			handle.Close())
		return fmt.Errorf("host: attach usage: %w", err)
	}
	automationStore, err := automations.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: close user db after automations attach failure",
			handle.Close())
		return fmt.Errorf("host: attach automations: %w", err)
	}
	metricStore, err := metricstore.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: close user db after metrics attach failure",
			handle.Close())
		return fmt.Errorf("host: attach metrics: %w", err)
	}
	// Long-term memory, skill lifecycle and the review queue share the
	// same handle: one user database, one migration, one write point
	// per feature.
	memoryStore, err := userstore.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: close user db after memory attach failure",
			handle.Close())
		return fmt.Errorf("host: attach user memory: %w", err)
	}
	skillUsageStore, err := skillusage.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: close user db after skill usage attach failure",
			handle.Close())
		return fmt.Errorf("host: attach skill usage: %w", err)
	}
	reviewStore, err := reviewstore.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: close user db after review queue attach failure",
			handle.Close())
		return fmt.Errorf("host: attach review queue: %w", err)
	}
	m.mu.Lock()
	if m.userDB != nil {
		m.mu.Unlock()
		telemetry.WarnErr(ctx, "host: close duplicate user db", handle.Close())
		return nil
	}
	m.userDB = handle
	m.userUsage = usageStore
	m.userAutomations = automationStore
	m.userMetrics = metricStore
	m.userMemory = memoryStore
	m.userSkillUsage = skillUsageStore
	m.userReview = reviewStore
	m.mu.Unlock()
	// Every memory write path refuses credential-shaped text, not just
	// the remember tool: the deploy document's secret rules are
	// compiled here and installed on the store itself.
	m.installMemoryTextGuard(ctx, userDir)
	m.pruneSkillUsage(ctx, userDir, skillUsageStore)
	// The stores reach the assemblies through the engine's external
	// dependencies, so the option builder has to be reinstalled once
	// they exist.
	m.refreshEngineOptions()
	return nil
}

// installMemoryTextGuard compiles the deploy document's secret rules
// and installs them on the user-memory store, so the settings card and
// an accepted review suggestion refuse the same text the remember tool
// refuses. Best-effort by design: a runtime whose document cannot be
// loaded keeps working (the remember tool still applies its own copy of
// the rules), and a missing guard must never keep user.db from opening.
func (m *Manager) installMemoryTextGuard(ctx context.Context, userDir string) {
	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		telemetry.WarnErr(ctx, "host: load config for memory guard failed", err)
		return
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		telemetry.WarnErr(ctx, "host: load document for memory guard failed", err)
		return
	}
	res, ok := view.Document.Resources["tool.remember"]
	if !ok {
		return
	}
	var settings struct {
		Redact assembly.RedactSettings `json:"redact"`
	}
	if err := json.Unmarshal(res.Settings, &settings); err != nil {
		telemetry.WarnErr(ctx, "host: decode remember redact settings failed", err)
		return
	}
	redact, err := assembly.CompileTextRedactor(settings.Redact)
	if err != nil {
		telemetry.WarnErr(ctx, "host: compile memory text guard failed", err)
		return
	}
	if redact == nil {
		return
	}
	userstore.SetTextRedactor(redact)
	telemetry.Info(ctx, "host: user memory text guard installed")
}

// pruneSkillUsage bounds the skill usage table once per user-database
// open: events older than the configured window are exactly the ones
// the curator's verdict ignores, so dropping them is what makes
// usage_window_days a bound rather than a comment. Best-effort by
// design — a failure only means the rows stay until the next start.
func (m *Manager) pruneSkillUsage(
	ctx context.Context, userDir string, store *skillusage.Store,
) {
	if store == nil {
		return
	}
	settings, err := config.LoadSkillLifecycle(userDir)
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: load skill lifecycle settings for prune failed", err)
		return
	}
	effective, err := settings.Resolve()
	if err != nil {
		telemetry.WarnErr(ctx,
			"host: resolve skill lifecycle settings for prune failed", err)
		return
	}
	if effective.UsageWindowDays <= 0 {
		return
	}
	before := time.Now().UTC().AddDate(0, 0, -effective.UsageWindowDays)
	// The prune outlives the call that opened the database.
	pruneCtx := context.WithoutCancel(ctx)
	go func() {
		rows, err := store.Prune(pruneCtx, before)
		if err != nil {
			telemetry.WarnErr(pruneCtx, "host: prune skill usage failed", err)
			return
		}
		if rows > 0 {
			telemetry.Info(pruneCtx, "host: skill usage pruned",
				otellog.Int64("rows", rows))
		}
	}()
}

// CloseUserDB closes the user-level database handle opened by
// OpenUserDB. Idempotent; safe to call even when OpenUserDB never
// succeeded.
func (m *Manager) CloseUserDB() {
	m.mu.Lock()
	handle := m.userDB
	m.userDB = nil
	m.userUsage = nil
	m.userAutomations = nil
	m.userMetrics = nil
	m.userMemory = nil
	m.userSkillUsage = nil
	m.userReview = nil
	m.mu.Unlock()
	if handle != nil {
		telemetry.WarnErr(context.Background(),
			"host: close user db failed", handle.Close())
	}
}
