package bindings

import (
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// SkillLifecycle is the skills page's view of the lifecycle half of the
// registry: how often each skill was used, the user's pin/retire
// decisions, and the archive records a retire left behind.
//
// Retirement is only ever performed through skills.Curator: it owns the
// snapshot, the archive record and the registry reload, so this layer
// cannot half-retire a skill.
type SkillLifecycle struct {
	core *core.Core
}

// NewSkillLifecycleBinding builds the skill lifecycle service.
func NewSkillLifecycleBinding(c *core.Core) *SkillLifecycle {
	return &SkillLifecycle{core: c}
}

// SkillUsageView is one skill row: the registry fields the page already
// shows, plus usage and the lifecycle decisions.
type SkillUsageView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Path        string `json:"path,omitempty"`
	Uses        int    `json:"uses"`
	LastUsed    string `json:"last_used,omitempty"`
	Pinned      bool   `json:"pinned"`
	Retired     bool   `json:"retired"`
	// Builtin skills ship with the app: they are shown but never a
	// retirement candidate and never retired.
	Builtin bool `json:"builtin"`
	// SuggestedRetire marks a curator candidate (idle and rarely used).
	SuggestedRetire bool   `json:"suggested_retire"`
	IdleDays        int    `json:"idle_days,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

// SkillArchiveView is one archive record: what a retire snapshotted and
// whether it has since been restored.
type SkillArchiveView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Scope       string `json:"scope,omitempty"`
	SkillPath   string `json:"skill_path"`
	ArchivePath string `json:"archive_path"`
	CreatedAt   string `json:"created_at,omitempty"`
	RestoredAt  string `json:"restored_at,omitempty"`
	// Restored reports whether this snapshot has already been put back.
	Restored bool `json:"restored"`
}

// SkillLifecycleState is the whole state of the lifecycle panel: the
// effective curator settings, the read-only bounds, and the rows.
type SkillLifecycleState struct {
	Enabled         bool `json:"enabled"`
	StaleAfterDays  int  `json:"stale_after_days"`
	MinUses         int  `json:"min_uses"`
	UsageWindowDays int  `json:"usage_window_days"`

	MinStaleAfterDays      int `json:"min_stale_after_days"`
	MaxStaleAfterDays      int `json:"max_stale_after_days"`
	DefaultStaleAfterDays  int `json:"default_stale_after_days"`
	MinMinUses             int `json:"min_min_uses"`
	MaxMinUses             int `json:"max_min_uses"`
	DefaultMinUses         int `json:"default_min_uses"`
	MinUsageWindowDays     int `json:"min_usage_window_days"`
	MaxUsageWindowDays     int `json:"max_usage_window_days"`
	DefaultUsageWindowDays int `json:"default_usage_window_days"`

	Skills   []SkillUsageView   `json:"skills"`
	Archives []SkillArchiveView `json:"archives"`
	// UsageAvailable reports whether a user database is open (without
	// one no usage is recorded and no skill can be retired).
	UsageAvailable bool `json:"usage_available"`
	// CuratorAvailable reports whether retirement is possible in this
	// runtime (it needs both the registry and the usage store).
	CuratorAvailable bool `json:"curator_available"`
}

// SkillLifecycleSettingsRequest is the save payload of the thresholds
// card. The curator reads them from the deploy document, so a save
// reloads it.
type SkillLifecycleSettingsRequest struct {
	Enabled         bool `json:"enabled"`
	StaleAfterDays  int  `json:"stale_after_days"`
	MinUses         int  `json:"min_uses"`
	UsageWindowDays int  `json:"usage_window_days"`
}

// skillsServiceOf resolves the live skills registry out of the
// assembled runtime, or false when the runtime is not ready.
func skillsServiceOf(c *core.Core) (*skills.Service, bool) {
	h := c.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return nil, false
	}
	value, ok := h.Controller().Runtime().Resource("skills")
	if !ok {
		return nil, false
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		return nil, false
	}
	return svc, true
}

// skillUsageStoreOf resolves the usage store out of a core, or false
// when this runtime has no user database.
func skillUsageStoreOf(c *core.Core) (skillusage.Lifecycle, bool) {
	manager := c.Runtime.Manager()
	if manager == nil {
		return nil, false
	}
	store := manager.SkillUsageStore()
	if store == nil || store.Empty() {
		return nil, false
	}
	return store, true
}

// requireSkillUsageStoreOf is the write-side guard for the pin
// decisions, which live in the same table.
func requireSkillUsageStoreOf(c *core.Core) (skillusage.Lifecycle, error) {
	store, ok := skillUsageStoreOf(c)
	if !ok {
		return nil, errdefs.NotAvailablef(
			"skill lifecycle: no user database in this runtime")
	}
	return store, nil
}

// curatorOf resolves the curator, or an error explaining why the
// runtime cannot retire anything. Enabled() already folds in "no
// registry" and "no user database".
func (b *SkillLifecycle) curatorOf() (*skills.Curator, error) {
	if svc, ok := skillsServiceOf(b.core); ok {
		if curator := svc.Curator(); curator.Enabled() {
			return curator, nil
		}
	}
	return nil, errdefs.NotAvailablef(
		"skill lifecycle: the skills curator is not available in this runtime")
}

// skillArchiveView maps one archive record for the page.
func skillArchiveView(archive skillusage.Archive) SkillArchiveView {
	return SkillArchiveView{
		ID:          archive.ID,
		Name:        archive.Name,
		Scope:       archive.Scope,
		SkillPath:   archive.SkillPath,
		ArchivePath: archive.ArchivePath,
		CreatedAt:   rfc3339(archive.CreatedAt),
		RestoredAt:  rfc3339(archive.RestoredAt),
		Restored:    !archive.RestoredAt.IsZero(),
	}
}

// SkillUsage returns the curator settings and one row per known skill,
// ordered by name. The rows are the union of the registry and the
// stored statistics: a runtime without a registry (or without a user
// database) still reports whatever half it has.
func (b *SkillLifecycle) SkillUsage() (SkillLifecycleState, error) {
	ctx := b.core.Shell.Context()
	loaded, err := config.LoadSkillLifecycle(b.core.UserDir)
	if err != nil {
		return SkillLifecycleState{}, err
	}
	effective, err := loaded.Resolve()
	if err != nil {
		return SkillLifecycleState{}, err
	}
	out := SkillLifecycleState{
		Enabled:                effective.Enabled,
		StaleAfterDays:         effective.StaleAfterDays,
		MinUses:                effective.MinUses,
		UsageWindowDays:        effective.UsageWindowDays,
		MinStaleAfterDays:      config.SkillLifecycleMinStaleAfterDays,
		MaxStaleAfterDays:      config.SkillLifecycleMaxStaleAfterDays,
		DefaultStaleAfterDays:  config.SkillLifecycleDefaultStaleAfterDays,
		MinMinUses:             config.SkillLifecycleMinMinUses,
		MaxMinUses:             config.SkillLifecycleMaxMinUses,
		DefaultMinUses:         config.SkillLifecycleDefaultMinUses,
		MinUsageWindowDays:     config.SkillLifecycleMinUsageWindowDays,
		MaxUsageWindowDays:     config.SkillLifecycleMaxUsageWindowDays,
		DefaultUsageWindowDays: config.SkillLifecycleDefaultUsageWindowDays,
		Skills:                 []SkillUsageView{},
		Archives:               []SkillArchiveView{},
	}

	// One row per skill name, so the registry and the statistics join
	// on the same key the store uses.
	rows := map[string]*SkillUsageView{}
	order := []string{}
	add := func(name string) *SkillUsageView {
		if row, ok := rows[name]; ok {
			return row
		}
		row := &SkillUsageView{Name: name}
		rows[name] = row
		order = append(order, name)
		return row
	}
	svc, registryOK := skillsServiceOf(b.core)
	if registryOK {
		for _, sk := range svc.List() {
			row := add(sk.Name)
			row.Description = sk.Description
			row.Scope = sk.Scope
			row.Path = sk.Path
			row.Builtin = sk.Scope == "builtin"
		}
	}

	if store, ok := skillUsageStoreOf(b.core); ok {
		out.UsageAvailable = true
		stats, err := store.Stats(ctx, time.Time{})
		if err != nil {
			return SkillLifecycleState{}, err
		}
		for _, stat := range stats {
			row := add(stat.Name)
			row.Uses = stat.Uses
			row.LastUsed = rfc3339(stat.LastUsed)
			if row.Scope == "" {
				row.Scope = stat.Scope
			}
		}
		states, err := store.States(ctx)
		if err != nil {
			return SkillLifecycleState{}, err
		}
		for name, state := range states {
			row := add(name)
			row.Pinned = state.Pinned
			row.Retired = state.Retired
			if row.Scope == "" {
				row.Scope = state.Scope
			}
		}
	}

	// The curator owns the verdict, so the candidates are taken from it
	// rather than recomputed here; a skill it does not call stale is
	// never marked stale in the page.
	if svc, ok := skillsServiceOf(b.core); ok {
		if curator := svc.Curator(); curator.Enabled() {
			out.CuratorAvailable = true
			candidates, err := curator.Candidates(ctx)
			if err != nil {
				return SkillLifecycleState{}, err
			}
			for _, candidate := range candidates {
				row := add(candidate.Name)
				row.SuggestedRetire = true
				row.IdleDays = candidate.IdleDays
				row.Reason = candidate.Reason
			}
			archives, err := curator.Archives(ctx)
			if err != nil {
				return SkillLifecycleState{}, err
			}
			for _, archive := range archives {
				out.Archives = append(out.Archives, skillArchiveView(archive))
			}
		}
	}

	out.Skills = make([]SkillUsageView, 0, len(order))
	for _, name := range order {
		out.Skills = append(out.Skills, *rows[name])
	}
	sort.Slice(out.Skills, func(i, j int) bool {
		if out.Skills[i].Name != out.Skills[j].Name {
			return out.Skills[i].Name < out.Skills[j].Name
		}
		return out.Skills[i].Scope < out.Skills[j].Scope
	})
	return out, nil
}

// PinSkill pins a skill: it is never a retirement candidate and the
// registry keeps offering it.
func (b *SkillLifecycle) PinSkill(name, scope string) error {
	return b.setPinned(name, scope, true)
}

// UnpinSkill drops the pin, letting the curator consider the skill
// again.
func (b *SkillLifecycle) UnpinSkill(name, scope string) error {
	return b.setPinned(name, scope, false)
}

func (b *SkillLifecycle) setPinned(name, scope string, pinned bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errdefs.Validationf("skill lifecycle: a skill name is required")
	}
	store, err := requireSkillUsageStoreOf(b.core)
	if err != nil {
		return err
	}
	_, err = store.SetPinned(
		b.core.Shell.Context(), name, strings.TrimSpace(scope), pinned)
	return err
}

// RetireSkill archives one skill through the curator and marks it
// retired. Nothing is deleted: the snapshot stays restorable.
func (b *SkillLifecycle) RetireSkill(
	name, scope string,
) (SkillArchiveView, error) {
	curator, err := b.curatorOf()
	if err != nil {
		return SkillArchiveView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return SkillArchiveView{}, errdefs.Validationf(
			"skill lifecycle: a skill name is required")
	}
	// The registry is the authority on a skill's scope. A mismatch
	// means the page is showing a stale row, and retiring the wrong
	// entry is the kind of mistake the archive cannot undo for the
	// user behind their back.
	if svc, ok := skillsServiceOf(b.core); ok {
		if sk, found := svc.ByName(name); found {
			if want := strings.TrimSpace(scope); want != "" && want != sk.Scope {
				return SkillArchiveView{}, errdefs.Validationf(
					"skill lifecycle: skill %q is %s-scoped, not %s",
					name, sk.Scope, want)
			}
		}
	}
	archive, err := curator.Retire(b.core.Shell.Context(), name)
	if err != nil {
		return SkillArchiveView{}, err
	}
	return skillArchiveView(archive), nil
}

// RestoreSkill puts one archived skill back and clears its retired
// flag.
func (b *SkillLifecycle) RestoreSkill(id string) (SkillArchiveView, error) {
	curator, err := b.curatorOf()
	if err != nil {
		return SkillArchiveView{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return SkillArchiveView{}, errdefs.Validationf(
			"skill lifecycle: an archive id is required")
	}
	archive, err := curator.Restore(b.core.Shell.Context(), id)
	if err != nil {
		return SkillArchiveView{}, err
	}
	return skillArchiveView(archive), nil
}

// SkillArchives lists the retirement snapshots, newest first. Without a
// curator the list is empty rather than an error.
func (b *SkillLifecycle) SkillArchives() ([]SkillArchiveView, error) {
	out := make([]SkillArchiveView, 0)
	svc, ok := skillsServiceOf(b.core)
	if !ok {
		return out, nil
	}
	curator := svc.Curator()
	if !curator.Enabled() {
		return out, nil
	}
	archives, err := curator.Archives(b.core.Shell.Context())
	if err != nil {
		return nil, err
	}
	for _, archive := range archives {
		out = append(out, skillArchiveView(archive))
	}
	return out, nil
}

// SaveSkillLifecycleSettings persists the curator thresholds and
// reloads the document so the curator picks them up without an app
// restart.
func (b *SkillLifecycle) SaveSkillLifecycleSettings(
	req SkillLifecycleSettingsRequest,
) error {
	enabled := req.Enabled
	settings := config.SkillLifecycleSettings{
		Enabled:         &enabled,
		StaleAfterDays:  req.StaleAfterDays,
		MinUses:         req.MinUses,
		UsageWindowDays: req.UsageWindowDays,
	}
	if err := config.SaveSkillLifecycle(b.core.UserDir, settings); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(host.WithAssemblyReason(
		b.core.Shell.Context(), host.ReasonSettingsSave))
}
