package skills

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/log"

	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
)

// ResourceKind is the deployable resource kind of the shared skills
// registry.
const ResourceKind = "opencraft.skills"

// Factory builds the opencraft.skills resource: discovery runs when
// the service is constructed and again on every Reload, consumed by
// the worldstate prepare hook and the skill_search / skill_read tools.
type Factory struct{}

var _ resource.Factory = Factory{}

// pluginRootsProvider is implemented by the shared plugin host
// (internal/capabilities/plugins/agent) and contributes plugin skill roots.
type pluginRootsProvider interface {
	SkillRoots() []string
}

// Spec declares the resource contract.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: "local",
		Deps: []resource.DepSpec{
			// Optional: enabled plugins may contribute skill roots.
			{Name: "plugin.host", Type: "opencraft.plugins", Required: false},
			// Optional: the user.db skill lifecycle store behind usage
			// recording, retirement and the curator.
			{Name: "skilllifecycle", Type: skillusage.ResourceKind, Required: false},
		},
	}
}

// Settings configures discovery. Paths are resolver-expanded from the
// engine assembly values (${ocraft:DATA_DIR}, ...).
type Settings struct {
	Enabled    *bool    `json:"enabled,omitempty"`
	UserDir    string   `json:"user_dir,omitempty"`
	TopN       int      `json:"top_n,omitempty"`
	MinScore   float64  `json:"min_score,omitempty"`
	ExtraRoots []string `json:"extra_roots,omitempty"`
	// Disabled lists skill names or SKILL.md paths to exclude
	// ([[skills.config]] enabled=false semantics).
	Disabled []string `json:"disabled,omitempty"`
	// ArchiveDir is where the curator writes its tar.gz snapshots.
	// Empty disables retiring (the archive would have nowhere to go).
	ArchiveDir string `json:"archive_dir,omitempty"`
}

// New builds the shared skills service.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf("opencraft skills: decode settings: %v", err)
	}
	enabled := true
	if settings.Enabled != nil {
		enabled = *settings.Enabled
	}
	extraRoots := append([]string(nil), settings.ExtraRoots...)
	if dep, ok := in.Dep("plugin.host"); ok {
		if p, ok := dep.(pluginRootsProvider); ok && p != nil {
			extraRoots = append(extraRoots, p.SkillRoots()...)
		}
	}
	svc := NewService(ctx, Options{
		UserDir:    settings.UserDir,
		Enabled:    enabled,
		TopN:       settings.TopN,
		MinScore:   settings.MinScore,
		ExtraRoots: extraRoots,
		Disabled:   settings.Disabled,
	})
	if dep, ok := in.Dep("skilllifecycle"); ok {
		if binding, ok := dep.(*skillusage.Binding); ok && binding != nil {
			svc.SetLifecycle(binding.Store, binding.Config, settings.ArchiveDir)
		}
	}
	// Accepted shape issues (a third-party name that differs from its
	// directory, for example) repeat on every runtime assembly, so they
	// are logged apart from real discovery failures and at a lower
	// severity.
	var errs, warnings []string
	for _, e := range svc.Errors() {
		if e.Warning {
			warnings = append(warnings, e.Path+": "+e.Message)
			continue
		}
		errs = append(errs, e.Path+": "+e.Message)
	}
	if len(errs) > 0 {
		telemetry.Warn(ctx, "skills: discovery errors",
			log.Int("count", len(errs)),
			log.String("errors", strings.Join(errs, "; ")))
	}
	if len(warnings) > 0 {
		telemetry.Info(ctx, "skills: discovery warnings",
			log.Int("count", len(warnings)),
			log.String("warnings", strings.Join(warnings, "; ")))
	}
	return svc, nil
}

// Register adds the opencraft.skills factory to r.
func Register(r *resource.Registry) error {
	return r.Register(Factory{})
}
