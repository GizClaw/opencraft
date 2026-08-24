// Package agents owns persistent subagents: create_agent /
// unregister_agent tools, the runtime wiring that registers them, and
// the ~/.opencraft/agents/<name>/agent.yaml declarations that survive
// restarts. A subagent is a flowcraft graph agent with its own system
// prompt; the caller only supplies the role and instructions.
package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/log"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/config"
)

// ResourceKind is the deployable resource kind of the persistent
// subagent registry.
const ResourceKind = "opencraft.agentlifecycle"

// Settings configures the registry. dir is env-expanded by the loader.
type Settings struct {
	Dir string `json:"dir"`
}

// AgentSpec is the persisted declaration of one subagent. It is the
// source of truth for the agent: everything needed to rebuild the
// runtime instance after a restart.
type AgentSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Graph is the complete flowcraft graph definition (JSON or YAML)
	// the agent runs on, including its system prompt. It is passed as
	// engine settings.graph verbatim.
	Graph     string    `json:"graph"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// Validate checks the user-supplied fields of a spec.
func (s AgentSpec) Validate() error {
	if err := validateAgentName(s.Name); err != nil {
		return err
	}
	if strings.TrimSpace(s.Description) == "" {
		return errdefs.Validationf(
			"agents: description is required (it identifies the agent in delegation targets)")
	}
	if strings.TrimSpace(s.Graph) == "" {
		return errdefs.Validationf(
			"agents: graph definition is required")
	}
	if err := validateGraphSyntax(s.Graph); err != nil {
		return errdefs.Validationf("agents: graph: %v", err)
	}
	return nil
}

// validateGraphSyntax checks that the graph definition parses as
// JSON/YAML. Structural validation (unique ids, entry presence, node
// config semantics) happens when the runtime builds the definition.
func validateGraphSyntax(graph string) error {
	var probe map[string]any
	if err := yaml.Unmarshal([]byte(graph), &probe); err != nil {
		return fmt.Errorf("parse graph definition: %w", err)
	}
	return nil
}

func validateAgentName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return errdefs.Validationf(
			"agents: name must be non-empty and must not have surrounding whitespace")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return errdefs.Validationf(
				"agents: name %q must be lowercase letters, digits, or hyphens", name)
		}
	}
	return nil
}

// registrar is the slice of Runtime that the lifecycle needs. The
// runtime satisfies it; tests inject a fake.
type registrar interface {
	RegisterAgent(
		ctx context.Context,
		name string,
		def agent.Definition,
		opts ...runtimecore.RegisterAgentOption,
	) (*agent.Agent, error)
	UnregisterAgent(
		ctx context.Context,
		name string,
		opts ...runtimecore.UnregisterAgentOption,
	) error
}

// Summary is the TUI / delegation-facing view of one persistent agent.
type Summary struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   string    `json:"created_at,omitempty"` // RFC3339 UTC
}

// Lifecycle creates, removes, and loads persistent subagents. The
// declaration directory is the source of truth; the runtime is only
// reached through the injected registrar, so the same instance serves
// every generation across reloads.
type Lifecycle struct {
	reg atomic.Pointer[registrar]
	dir string
}

// New creates the lifecycle rooted at dir (usually
// ~/.opencraft/agents).
func New(dir string) (*Lifecycle, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errdefs.Validationf("agents: directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agents: create directory: %w", err)
	}
	return &Lifecycle{dir: dir}, nil
}

// Bind installs the runtime registrar (Build's *runtimecore.Runtime).
// Call once after Build; reloads keep the same registrar.
func (l *Lifecycle) Bind(reg registrar) { l.reg.Store(&reg) }

func (l *Lifecycle) registrar() registrar {
	if stored := l.reg.Load(); stored != nil {
		return *stored
	}
	return nil
}

// CreateResult reports a successful creation.
type CreateResult struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	PersistedTo string    `json:"persisted_to"`
	CreatedAt   time.Time `json:"created_at"`
}

// Create validates the spec, registers the graph agent in the runtime,
// and persists the declaration. On a persistence failure the runtime
// registration is rolled back so the two stay consistent.
func (l *Lifecycle) Create(ctx context.Context, spec AgentSpec) (CreateResult, error) {
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = time.Now().UTC()
	}
	if err := spec.Validate(); err != nil {
		return CreateResult{}, err
	}
	reg := l.registrar()
	if reg == nil {
		return CreateResult{}, errdefs.NotAvailablef("agents: runtime not ready")
	}
	def := agentDefinition(spec)
	if _, err := reg.RegisterAgent(
		ctx, spec.Name, def,
		runtimecore.WithToolAssembly(toolAssemblyResource),
	); err != nil {
		return CreateResult{}, fmt.Errorf("agents: register %q: %w", spec.Name, err)
	}
	if err := l.writeSpec(spec); err != nil {
		// Roll back the runtime registration: the disk never became
		// the source of truth, so the agent must not outlive the turn.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), removeTimeout)
		defer cancel()
		if unregErr := reg.UnregisterAgent(
			rollbackCtx, spec.Name,
			runtimecore.WithRemoveTimeout(removeTimeout),
		); unregErr != nil {
			telemetry.Error(ctx, "agents: rollback registration after persist failure",
				log.String("agent", spec.Name),
				log.String("persist_error", err.Error()),
				log.String("rollback_error", unregErr.Error()))
		}
		return CreateResult{}, fmt.Errorf("agents: persist %q: %w", spec.Name, err)
	}
	return CreateResult{
		Name:        spec.Name,
		Description: spec.Description,
		PersistedTo: l.agentDir(spec.Name),
		CreatedAt:   spec.CreatedAt,
	}, nil
}

// Remove unregisters the agent (draining in-flight delegations) and
// deletes its persisted directory. A drain failure keeps both the
// runtime registration and the files intact so the call is retryable.
func (l *Lifecycle) Remove(ctx context.Context, name string) error {
	if err := validateAgentName(name); err != nil {
		return err
	}
	reg := l.registrar()
	if reg == nil {
		return errdefs.NotAvailablef("agents: runtime not ready")
	}
	if err := reg.UnregisterAgent(
		ctx, name,
		runtimecore.WithRemoveTimeout(removeTimeout),
	); err != nil {
		return fmt.Errorf("agents: unregister %q: %w", name, err)
	}
	dir := l.agentDir(name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf(
			"agents: %q unregistered but its declaration %s could not be removed; "+
				"it will be re-registered on the next start unless removed manually: %w",
			name, dir, err)
	}
	return nil
}

// LoadAll registers every persisted declaration. Errors are returned
// individually (via []LoadError) and never fail startup: a broken or
// conflicting declaration must not block the runtime.
type LoadError struct {
	Name string
	Err  error
}

func (e LoadError) Error() string { return fmt.Sprintf("%s: %v", e.Name, e.Err) }

func (l *Lifecycle) LoadAll(ctx context.Context) []LoadError {
	reg := l.registrar()
	if reg == nil {
		return []LoadError{{Err: errdefs.NotAvailablef("agents: runtime not ready")}}
	}
	var failures []LoadError
	for _, dir := range l.scanDirs() {
		spec, err := l.readSpec(dir)
		if err != nil {
			failures = append(failures, LoadError{Name: filepath.Base(dir), Err: err})
			continue
		}
		if _, err := reg.RegisterAgent(
			ctx, spec.Name, agentDefinition(spec),
			runtimecore.WithToolAssembly(toolAssemblyResource),
		); err != nil {
			failures = append(failures, LoadError{Name: spec.Name, Err: err})
			continue
		}
	}
	return failures
}

// List returns every persisted agent, sorted by name.
func (l *Lifecycle) List() []Summary {
	var out []Summary
	for _, dir := range l.scanDirs() {
		spec, err := l.readSpec(dir)
		if err != nil {
			continue
		}
		out = append(out, Summary{
			Name:        spec.Name,
			Description: spec.Description,
			CreatedAt:   spec.CreatedAt.Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (l *Lifecycle) agentDir(name string) string {
	return filepath.Join(l.dir, name)
}

func (l *Lifecycle) scanDirs() []string {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(l.dir, entry.Name()))
	}
	sort.Strings(dirs)
	return dirs
}

const specFile = "agent.yaml"

func (l *Lifecycle) writeSpec(spec AgentSpec) error {
	data, err := yaml.Marshal(spec)
	if err != nil {
		return err
	}
	dir := l.agentDir(spec.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, specFile)); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (l *Lifecycle) readSpec(dir string) (AgentSpec, error) {
	var spec AgentSpec
	data, err := os.ReadFile(filepath.Join(dir, specFile))
	if err != nil {
		return spec, err
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return spec, err
	}
	if err := spec.Validate(); err != nil {
		return spec, err
	}
	return spec, nil
}

// DefaultAgentsDir returns ~/.opencraft/agents, creating it.
func DefaultAgentsDir() (string, error) {
	data, err := config.UserDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// removeTimeout bounds UnregisterAgent drains from this package.
const removeTimeout = 30 * time.Second
