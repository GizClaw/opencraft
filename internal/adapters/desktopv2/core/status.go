package core

import (
	"github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// ConfigStatus is the application configuration state shared by the
// "ready" UI event and the Config binding.
type ConfigStatus struct {
	Needed           bool   `json:"needed"`
	DefaultModel     string `json:"default_model"`
	DefaultReasoning bool   `json:"default_reasoning"`
	WorkDir          string `json:"work_dir"`
	UserDir          string `json:"user_dir"`
	Version          string `json:"version"`
	Agents           int    `json:"agents"`
}

// ConfigStatus snapshots the current configuration state. The default
// reasoning flag follows the same empty-hint rule the runtime uses:
// the first enabled instance's first model.
func (c *Core) ConfigStatus() ConfigStatus {
	configured := false
	if mgr, err := config.Open(config.Options{UserDir: c.UserDir}); err == nil {
		if view, err := mgr.Load(c.Shell.Context()); err == nil {
			configured, _ = config.RouterConfigured(view.Document)
		}
	}
	defaultReasoning := false
	if cfg, err := config.LoadInference(c.UserDir); err == nil {
		defaultReasoning = cfg.ModelReasoning("")
	}
	st := ConfigStatus{
		Needed:           !configured,
		DefaultModel:     config.DefaultModel(c.UserDir),
		DefaultReasoning: defaultReasoning,
		WorkDir:          c.ActiveWorkDir(),
		UserDir:          c.UserDir,
		Version:          telemetry.ServiceVersion,
	}
	if h := c.Runtime.Current(); h != nil && h.Agents() != nil {
		st.Agents = len(h.Agents().List())
	}
	return st
}
