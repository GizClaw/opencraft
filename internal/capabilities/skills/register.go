package skills

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/workspace"
	"go.opentelemetry.io/otel/log"
)

// ResourceKind is the deployable resource kind of the shared skills
// registry.
const ResourceKind = "opencraft.skills"

// Factory builds the opencraft.skills resource: one discovery pass
// cached for the process lifetime, consumed by the worldstate prepare
// hook and the skill_search / skill_read tools.
type Factory struct{}

var _ resource.Factory = Factory{}

// pluginRootsProvider is implemented by the shared plugin host
// (internal/plugins/agent) and contributes plugin skill roots.
type pluginRootsProvider interface {
	SkillRoots() []string
}

// Spec declares the resource contract.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: "local",
		Deps: []resource.DepSpec{
			// Optional: in-root reads go through the workspace
			// abstraction when present (local deployments usually
			// omit it and read the host filesystem directly).
			{Name: "workspace", Type: "workspace.Workspace", Required: false},
			// Optional: enabled plugins may contribute skill roots.
			{Name: "plugin.host", Type: "opencraft.plugins", Required: false},
		},
	}
}

// Settings configures discovery. Paths are env-expanded by the loader
// (${env:OPEN_CRAFT_WORKDIR}, ${env:OPEN_CRAFT_DATA_DIR}, ...).
type Settings struct {
	Enabled    *bool    `json:"enabled,omitempty"`
	WorkDir    string   `json:"work_dir"`
	UserDir    string   `json:"user_dir,omitempty"`
	TopN       int      `json:"top_n,omitempty"`
	MinScore   float64  `json:"min_score,omitempty"`
	ExtraRoots []string `json:"extra_roots,omitempty"`
	// Disabled lists skill names or SKILL.md paths to exclude
	// ([[skills.config]] enabled=false semantics).
	Disabled []string `json:"disabled,omitempty"`
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
	var ws workspace.Workspace
	if dep, ok := in.Dep("workspace"); ok {
		if w, ok := dep.(workspace.Workspace); ok {
			ws = w
		}
	}
	extraRoots := append([]string(nil), settings.ExtraRoots...)
	if dep, ok := in.Dep("plugin.host"); ok {
		if p, ok := dep.(pluginRootsProvider); ok && p != nil {
			extraRoots = append(extraRoots, p.SkillRoots()...)
		}
	}
	svc := NewService(ctx, Options{
		WorkBase:   settings.WorkDir,
		UserDir:    settings.UserDir,
		Workspace:  ws,
		Enabled:    enabled,
		TopN:       settings.TopN,
		MinScore:   settings.MinScore,
		ExtraRoots: extraRoots,
		Disabled:   settings.Disabled,
	})
	if errs := svc.Errors(); len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Path+": "+e.Message)
		}
		telemetry.Warn(ctx, "skills: discovery errors",
			log.Int("count", len(errs)),
			log.String("errors", strings.Join(msgs, "; ")))
	}
	return svc, nil
}

// Register adds the opencraft.skills factory to r.
func Register(r *resource.Registry) error {
	return r.Register(Factory{})
}
