// Package agents owns persistent subagents: create_agent /
// update_agent / unregister_agent tools, the runtime wiring that
// registers them, and the ~/.opencraft/agents/<name>/agent.yaml
// declarations that survive restarts. A subagent is a flowcraft graph
// agent with its own system prompt; the caller only supplies the role
// and instructions.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/log"
	"sigs.k8s.io/yaml"
)

// ResourceKind is the deployable resource kind of the persistent
// subagent registry.
const ResourceKind = "opencraft.agentlifecycle"

// specVersion is the current on-disk declaration format. Declarations
// carry the version explicitly so future format changes can migrate by
// version instead of guessing from the field shape.
const specVersion = 1

// Settings configures the registry. Paths are resolver-expanded from
// the engine assembly values (${ocraft:...}) before the factory
// decodes them.
type Settings struct {
	Dir string `json:"dir"`
	// WorkDir/UserDir mirror the assistant's prepare-hook context.
	// Subagent definitions embed them verbatim: dynamic registration
	// (runtime.RegisterAgent) expands settings without the builder's
	// custom resolver, so references cannot survive into the
	// definition. The resource settings carry the same ${ocraft:...}
	// references and are expanded during the deployment build, which
	// keeps the values host-injected rather than self-derived.
	WorkDir string `json:"work_dir,omitempty"`
	UserDir string `json:"user_dir,omitempty"`
}

// AgentSpec is the persisted declaration of one subagent. It is the
// source of truth for the agent: the Card carries the delegation
// identity, and Engine.Settings carries the graph source. Files store
// the graph as an inline YAML object under engine.settings.graph so
// they can be authored by hand; the shape mirrors the flowcraft
// agent.Definition subset that lives under agents.<name> in a deploy
// document. Host-owned wiring (engine deps, build knobs, the
// worldstate prepare hook, and path injection) is never persisted:
// agentDefinition rebuilds it on every registration, so a declaration
// cannot override memory/workspace/execpolicy wiring.
type AgentSpec struct {
	// Version is the on-disk declaration format. Version 1 stores the
	// graph inline under engine.settings.graph; files without the
	// field are pre-version declarations normalized on read.
	Version int `json:"version,omitempty"`
	// Card is the agent identity card: Name is the delegation id
	// (lowercase letters, digits, hyphens) and Description is the
	// summary shown in delegation targets.
	Card agent.AgentCard `json:"card"`
	// Engine mirrors the flowcraft engine reference. Only kind/impl
	// ("agent.Engine"/"graph") and settings.graph are persisted; deps
	// stay empty because the Host owns the binding.
	Engine    agent.EngineRef `json:"engine"`
	CreatedAt time.Time       `json:"created_at,omitempty"`
}

// NewSpec builds a declaration from the delegation identity and the
// caller-supplied graph source (JSON or YAML text). It is the only
// constructor callers should use: the engine reference is fixed to the
// graph engine and carries the graph source exactly as the tool/UI
// exchange it.
func NewSpec(name, description, graph string) AgentSpec {
	return AgentSpec{
		Version: specVersion,
		Card: agent.AgentCard{
			Name:        name,
			Description: description,
		},
		Engine: agent.EngineRef{
			Kind:     "agent.Engine",
			Impl:     "graph",
			Settings: graphSettings(graph),
		},
	}
}

// graphSettings encodes the graph source as engine settings
// (settings.graph). json.Marshal of a string map cannot fail.
func graphSettings(graph string) json.RawMessage {
	raw, err := json.Marshal(map[string]string{"graph": graph})
	if err != nil {
		// Unreachable: the value is one string field.
		telemetry.WarnErr(context.Background(),
			"agents: marshal graph settings failed", err)
		return nil
	}
	return raw
}

// GraphText returns the graph source as text (inline objects are
// rendered as YAML). It is what the desktop graph editor parses;
// registration embeds the parsed graph directly into engine settings.
func (s AgentSpec) GraphText() (string, error) {
	graph, err := decodeGraphOnly(s.Engine.Settings)
	if err != nil {
		return "", err
	}
	text, err := graphText(graph)
	if err != nil {
		return "", err
	}
	return text, nil
}

// Validate checks the user-supplied fields of a spec.
func (s AgentSpec) Validate() error {
	if s.Version != 0 && s.Version != specVersion {
		return errdefs.Validationf(
			"agents: unsupported declaration version %d (current is %d)",
			s.Version, specVersion)
	}
	if err := validateAgentName(s.Card.Name); err != nil {
		return err
	}
	if strings.TrimSpace(s.Card.Description) == "" {
		return errdefs.Validationf(
			"agents: description is required (it identifies the agent in delegation targets)")
	}
	if s.Engine.Kind != "agent.Engine" || s.Engine.Impl != "graph" {
		return errdefs.Validationf(
			"agents: engine must be agent.Engine/graph (declarations cannot select another engine)")
	}
	if len(s.Engine.Deps) > 0 {
		return errdefs.Validationf(
			"agents: engine deps are host-owned and must be empty in the declaration")
	}
	graph, err := decodeGraphOnly(s.Engine.Settings)
	if err != nil {
		return errdefs.Validationf("agents: %v", err)
	}
	if _, err := graphToMap(graph); err != nil {
		return errdefs.Validationf("agents: graph: %v", err)
	}
	return nil
}

// decodeGraphOnly returns engine.settings.graph and rejects any other
// settings key: graph is the only user-owned engine setting; build
// knobs and other wiring stay Host-injected.
func decodeGraphOnly(settings json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		return nil, errdefs.Validationf(
			"engine.settings is required and may only contain graph")
	}
	var envelope struct {
		Graph json.RawMessage `json:"graph"`
	}
	dec := json.NewDecoder(bytes.NewReader(settings))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return nil, errdefs.Validationf(
			"engine.settings: %v", err)
	}
	if len(bytes.TrimSpace(envelope.Graph)) == 0 {
		return nil, errdefs.Validationf(
			"engine.settings.graph is required")
	}
	return envelope.Graph, nil
}

// graphToMap parses settings.graph, accepting either a JSON string of
// graph text (JSON or YAML, the tool/UI wire shape) or an inline YAML
// object (the hand-authored file shape). Numbers are decoded with
// json.Number so integer literals beyond 2^53 survive the round trip
// through normalization instead of being rounded via float64.
func graphToMap(raw json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errdefs.Validationf("graph is empty")
	}
	var graph map[string]any
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, fmt.Errorf("decode graph source: %w", err)
		}
		data, err := yaml.YAMLToJSON([]byte(text))
		if err != nil {
			return nil, fmt.Errorf("parse graph definition: %w", err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&graph); err != nil {
			return nil, fmt.Errorf("parse graph definition: %w", err)
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&graph); err != nil {
			return nil, fmt.Errorf("parse graph definition: %w", err)
		}
	}
	if len(graph) == 0 {
		return nil, errdefs.Validationf("graph definition is empty")
	}
	return graph, nil
}

// graphText renders settings.graph as text for the graph editor and
// engine assembly: inline objects are emitted as YAML, string sources
// pass through unchanged.
func graphText(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	graph, err := graphToMap(raw)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(graph)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// normalizeSpec canonicalizes a declaration for registration and
// storage: the graph is parsed into an inline object under
// engine.settings.graph and the format version is stamped.
func normalizeSpec(spec AgentSpec) (AgentSpec, error) {
	graph, err := decodeGraphOnly(spec.Engine.Settings)
	if err != nil {
		return spec, err
	}
	object, err := graphToMap(graph)
	if err != nil {
		return spec, err
	}
	objectJSON, err := json.Marshal(object)
	if err != nil {
		return spec, err
	}
	settings, err := json.Marshal(map[string]json.RawMessage{
		"graph": objectJSON,
	})
	if err != nil {
		return spec, err
	}
	spec.Engine.Settings = settings
	spec.Version = specVersion
	return spec, nil
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
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at,omitempty"` // RFC3339 UTC
}

// Lifecycle creates, removes, and loads persistent subagents. The
// declaration directory is the source of truth; the runtime is only
// reached through the injected registrar. Assembly builds one
// instance per runtime generation, so reloads hand a fresh instance
// to the Host; AdoptKnown carries registration knowledge across
// generations.
type Lifecycle struct {
	reg atomic.Pointer[registrar]
	// known tracks every agent this process registered from disk or
	// through create_agent. Reloads carry the set into the new
	// generation's lifecycle so LoadMissing can retry broken/new
	// declarations without replaying already-registered ones into the
	// live registry (flowcraft Runtime.Reload re-binds those).
	mu    sync.Mutex
	known map[string]struct{}
	dir   string
	work  string
	user  string
}

// New creates the lifecycle rooted at dir (usually
// ~/.opencraft/agents). work/user are the assembly paths subagent
// definitions embed into their prepare hook (see Settings).
func New(dir, workDir, userDir string) (*Lifecycle, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errdefs.Validationf("agents: directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agents: create directory: %w", err)
	}
	return &Lifecycle{
		known: make(map[string]struct{}),
		dir:   dir,
		work:  workDir,
		user:  userDir,
	}, nil
}

// Bind installs the runtime registrar (Build's *runtimecore.Runtime)
// on this instance. The Host binds each generation's lifecycle when
// it is assembled and again after an in-place reload.
func (l *Lifecycle) Bind(reg registrar) { l.reg.Store(&reg) }

func (l *Lifecycle) registrar() registrar {
	if stored := l.reg.Load(); stored != nil {
		return *stored
	}
	return nil
}

func (l *Lifecycle) markKnown(name string) {
	l.mu.Lock()
	l.known[name] = struct{}{}
	l.mu.Unlock()
}

func (l *Lifecycle) forgetKnown(name string) {
	l.mu.Lock()
	delete(l.known, name)
	l.mu.Unlock()
}

func (l *Lifecycle) isKnown(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.known[name]
	return ok
}

// AdoptKnown carries the registration knowledge of the previous
// generation's lifecycle into this one so LoadMissing does not replay
// agents that flowcraft Runtime.Reload has already re-bound. Host
// calls this on every in-place reload before LoadMissing.
func (l *Lifecycle) AdoptKnown(from *Lifecycle) {
	if from == nil || from == l {
		return
	}
	from.mu.Lock()
	defer from.mu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	for name := range from.known {
		l.known[name] = struct{}{}
	}
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
	spec, err := normalizeSpec(spec)
	if err != nil {
		return CreateResult{}, errdefs.Validationf(
			"agents: normalize declaration: %v", err)
	}
	if err := spec.Validate(); err != nil {
		return CreateResult{}, err
	}
	reg := l.registrar()
	if reg == nil {
		return CreateResult{}, errdefs.NotAvailablef("agents: runtime not ready")
	}
	def, err := l.agentDefinition(spec)
	if err != nil {
		return CreateResult{}, fmt.Errorf(
			"agents: assemble definition %q: %w", spec.Card.Name, err)
	}
	if _, err := reg.RegisterAgent(
		ctx, spec.Card.Name, def,
		runtimecore.WithToolAssembly(toolAssemblyResource),
	); err != nil {
		return CreateResult{}, fmt.Errorf("agents: register %q: %w", spec.Card.Name, err)
	}
	if err := l.writeSpec(spec); err != nil {
		// Roll back the runtime registration: the disk never became
		// the source of truth, so the agent must not outlive the turn.
		rollbackCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), removeTimeout)
		defer cancel()
		if unregErr := reg.UnregisterAgent(
			rollbackCtx, spec.Card.Name,
			runtimecore.WithRemoveTimeout(removeTimeout),
		); unregErr != nil {
			telemetry.Error(ctx, "agents: rollback registration after persist failure",
				log.String("agent", spec.Card.Name),
				log.String("persist_error", err.Error()),
				log.String("rollback_error", unregErr.Error()))
		} else {
			l.forgetKnown(spec.Card.Name)
		}
		return CreateResult{}, fmt.Errorf("agents: persist %q: %w", spec.Card.Name, err)
	}
	l.markKnown(spec.Card.Name)
	return l.resultFor(l.agentDir(spec.Card.Name), spec), nil
}

// Update applies a partial change to an existing subagent: non-empty
// description/graph values replace the persisted ones. The name is
// immutable (renaming means remove + create). The runtime registration
// is swapped for the new definition after in-flight delegations drain,
// then the declaration is rewritten; on any failure the old
// registration and declaration are restored so the two stay
// consistent. A call that changes nothing is a no-op.
func (l *Lifecycle) Update(
	ctx context.Context,
	name, description, graph string,
) (CreateResult, error) {
	if err := validateAgentName(name); err != nil {
		return CreateResult{}, err
	}
	if strings.TrimSpace(description) == "" && strings.TrimSpace(graph) == "" {
		return CreateResult{}, errdefs.Validationf(
			"agents: update %q: nothing to update (provide description and/or graph)", name)
	}
	reg := l.registrar()
	if reg == nil {
		return CreateResult{}, errdefs.NotAvailablef("agents: runtime not ready")
	}

	dir := l.agentDir(name)
	old, err := l.readSpec(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return CreateResult{}, errdefs.NotFoundf(
				"agents: %q is not a persisted agent", name)
		}
		return CreateResult{}, fmt.Errorf("agents: read %q declaration: %w", name, err)
	}

	updated := old
	if strings.TrimSpace(description) != "" {
		updated.Card.Description = description
	}
	if strings.TrimSpace(graph) != "" {
		updated.Engine.Settings = graphSettings(graph)
	}
	normalized, err := normalizeSpec(updated)
	if err != nil {
		return CreateResult{}, errdefs.Validationf(
			"agents: normalize declaration %q: %v", name, err)
	}
	updated = normalized
	oldGraph, err := old.GraphText()
	if err != nil {
		return CreateResult{}, errdefs.Validationf(
			"agents: render current graph of %q: %v", name, err)
	}
	updatedGraph, err := updated.GraphText()
	if err != nil {
		return CreateResult{}, errdefs.Validationf(
			"agents: render updated graph of %q: %v", name, err)
	}
	if old.Card.Description == updated.Card.Description &&
		oldGraph == updatedGraph {
		// No field actually changed: the agent is already current, so
		// skip the drain/swap/write entirely.
		return l.resultFor(dir, updated), nil
	}
	if err := updated.Validate(); err != nil {
		return CreateResult{}, err
	}
	def, err := l.agentDefinition(updated)
	if err != nil {
		return CreateResult{}, fmt.Errorf(
			"agents: assemble definition %q: %w", name, err)
	}

	// Swap the live registration: drain in-flight delegations first,
	// then register the new definition. On failure restore the old one.
	if err := reg.UnregisterAgent(
		ctx, name, runtimecore.WithRemoveTimeout(removeTimeout),
	); err != nil {
		return CreateResult{}, fmt.Errorf("agents: unregister %q for update: %w", name, err)
	}
	if _, err := reg.RegisterAgent(
		ctx, name, def,
		runtimecore.WithToolAssembly(toolAssemblyResource),
	); err != nil {
		l.restoreAfterFailedUpdate(ctx, name, old, err)
		return CreateResult{}, fmt.Errorf("agents: register updated %q: %w", name, err)
	}
	if err := l.writeSpec(updated); err != nil {
		l.restoreAfterFailedUpdate(ctx, name, old, err)
		return CreateResult{}, fmt.Errorf("agents: persist updated %q: %w", name, err)
	}
	return l.resultFor(dir, updated), nil
}

// Detail returns the persisted declaration of one subagent, including
// its graph definition, so hosts can visualize and edit the live
// definition without re-parsing the list.
func (l *Lifecycle) Detail(ctx context.Context, name string) (AgentSpec, error) {
	if err := validateAgentName(name); err != nil {
		return AgentSpec{}, err
	}
	dir := l.agentDir(name)
	spec, err := l.readSpec(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return AgentSpec{}, errdefs.NotFoundf(
				"agents: %q is not a persisted agent", name)
		}
		return AgentSpec{}, fmt.Errorf("agents: read %q declaration: %w",
			name, err)
	}
	return spec, nil
}

// restoreAfterFailedUpdate re-registers the previous declaration after
// an update failed partway (the new registration or the disk write did
// not complete). The name may or may not still be registered at this
// point: if the swap registration failed it is free, and if the disk
// write failed it holds the new definition. Unregistering first covers
// both cases (unknown names are an idempotent no-op), so the old
// declaration can always be re-registered. Restore failures are logged
// but the primary error is what the caller sees.
func (l *Lifecycle) restoreAfterFailedUpdate(
	ctx context.Context,
	name string,
	old AgentSpec,
	updateErr error,
) {
	rollbackCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), removeTimeout)
	defer cancel()
	if err := l.registrar().UnregisterAgent(
		rollbackCtx, name,
		runtimecore.WithRemoveTimeout(removeTimeout),
	); err != nil {
		telemetry.Error(ctx, "agents: unregister failed definition during restore",
			log.String("agent", name),
			log.String("update_error", updateErr.Error()),
			log.String("restore_error", err.Error()))
		return
	}
	def, err := l.agentDefinition(old)
	if err != nil {
		telemetry.Error(ctx, "agents: assemble restored definition failed",
			log.String("agent", name),
			log.String("update_error", updateErr.Error()),
			log.String("restore_error", err.Error()))
		return
	}
	if _, err := l.registrar().RegisterAgent(
		rollbackCtx, old.Card.Name, def,
		runtimecore.WithToolAssembly(toolAssemblyResource),
	); err != nil {
		telemetry.Error(ctx, "agents: restore registration after update failure",
			log.String("agent", name),
			log.String("update_error", updateErr.Error()),
			log.String("restore_error", err.Error()))
	}
}

func (l *Lifecycle) resultFor(dir string, spec AgentSpec) CreateResult {
	return CreateResult{
		Name:        spec.Card.Name,
		Description: spec.Card.Description,
		PersistedTo: dir,
		CreatedAt:   spec.CreatedAt,
	}
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
	l.forgetKnown(name)
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
		def, err := l.agentDefinition(spec)
		if err != nil {
			failures = append(failures, LoadError{Name: spec.Card.Name, Err: err})
			continue
		}
		if _, err := reg.RegisterAgent(
			ctx, spec.Card.Name, def,
			runtimecore.WithToolAssembly(toolAssemblyResource),
		); err != nil {
			failures = append(failures, LoadError{Name: spec.Card.Name, Err: err})
			continue
		}
		l.markKnown(spec.Card.Name)
	}
	return failures
}

// LoadMissing registers declarations that are not already live. Host
// uses it after an in-place reload: AdoptKnown carries the previous
// generation's registrations (which flowcraft Runtime.Reload re-binds
// itself), so LoadMissing only retries declarations that failed at
// cold start or appeared on disk since — the repair path for
// hand-authored agent.yaml files. A declaration whose name is already
// registered counts as live: flowcraft's reload re-bind (or a Create
// that raced the known-set handoff) may have registered it after
// AdoptKnown ran, so LoadMissing records it as known instead of
// surfacing a conflict on every reload.
func (l *Lifecycle) LoadMissing(ctx context.Context) []LoadError {
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
		if l.isKnown(spec.Card.Name) {
			continue
		}
		def, err := l.agentDefinition(spec)
		if err != nil {
			failures = append(failures, LoadError{Name: spec.Card.Name, Err: err})
			continue
		}
		if _, err := reg.RegisterAgent(
			ctx, spec.Card.Name, def,
			runtimecore.WithToolAssembly(toolAssemblyResource),
		); err != nil {
			if errdefs.IsConflict(err) {
				// The declaration is already live in the runtime, so
				// there is nothing to repair. Remember the name so
				// later reloads (which adopt this lifecycle's known
				// set) stop retrying it.
				l.markKnown(spec.Card.Name)
				continue
			}
			failures = append(failures, LoadError{Name: spec.Card.Name, Err: err})
			continue
		}
		l.markKnown(spec.Card.Name)
	}
	return failures
}

// List returns every persisted agent, sorted by name.
func (l *Lifecycle) List() []Summary {
	out := []Summary{}
	for _, dir := range l.scanDirs() {
		spec, err := l.readSpec(dir)
		if err != nil {
			continue
		}
		out = append(out, Summary{
			Name:        spec.Card.Name,
			Description: spec.Card.Description,
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
	normalized, err := normalizeSpec(spec)
	if err != nil {
		return err
	}
	spec = normalized
	data, err := yaml.Marshal(spec)
	if err != nil {
		return err
	}
	dir := l.agentDir(spec.Card.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		telemetry.WarnErr(context.Background(),
			"agents: close agent spec temp after write failure", tmp.Close())
		telemetry.WarnErr(context.Background(),
			"agents: remove agent spec temp after write failure",
			os.Remove(tmpName))
		return err
	}
	if err := tmp.Sync(); err != nil {
		telemetry.WarnErr(context.Background(),
			"agents: close agent spec temp after sync failure", tmp.Close())
		telemetry.WarnErr(context.Background(),
			"agents: remove agent spec temp after sync failure",
			os.Remove(tmpName))
		return err
	}
	if err := tmp.Close(); err != nil {
		telemetry.WarnErr(context.Background(),
			"agents: remove agent spec temp after close failure",
			os.Remove(tmpName))
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, specFile)); err != nil {
		telemetry.WarnErr(context.Background(),
			"agents: remove agent spec temp after rename failure",
			os.Remove(tmpName))
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
	if legacy, ok, err := decodeLegacySpec(data); err != nil {
		return spec, fmt.Errorf("agents: parse %s: %w", dir, err)
	} else if ok {
		// Pre-version declarations (name/description/graph at the top
		// level) still load; the file is rewritten in the current
		// format on the next update.
		spec = legacy
	} else {
		// Hand-authored files are strict: unknown keys (prepare,
		// policy, tools, observe, ...) are rejected instead of being
		// silently dropped, so a declaration can never look like it
		// controls Host-owned wiring.
		if err := yaml.UnmarshalStrict(data, &spec); err != nil {
			return spec, fmt.Errorf("agents: parse %s: %w", dir, err)
		}
		if spec.Version != 0 && spec.Version != specVersion {
			return spec, fmt.Errorf(
				"agents: %s: unsupported declaration version %d (current is %d)",
				dir, spec.Version, specVersion)
		}
	}
	spec, err = normalizeSpec(spec)
	if err != nil {
		return spec, fmt.Errorf("agents: normalize %s: %w", dir, err)
	}
	if err := spec.Validate(); err != nil {
		return spec, fmt.Errorf("agents: validate %s: %w", dir, err)
	}
	if spec.Card.Name != filepath.Base(dir) {
		return spec, errdefs.Validationf(
			"agents: declaration %s names %q; the directory name is the identity",
			dir, spec.Card.Name)
	}
	return spec, nil
}

// decodeLegacySpec converts a pre-Definition declaration (top-level
// name/description/graph, written before the card/engine format) into
// the current AgentSpec. ok=false means the file is not a legacy
// declaration.
func decodeLegacySpec(data []byte) (AgentSpec, bool, error) {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return AgentSpec{}, false, err
	}
	if _, ok := probe["graph"].(string); !ok {
		return AgentSpec{}, false, nil
	}
	if _, hasCard := probe["card"]; hasCard {
		return AgentSpec{}, false, nil
	}
	if _, hasVersion := probe["version"]; hasVersion {
		return AgentSpec{}, false, nil
	}
	var legacy struct {
		Name        string    `json:"name"`
		Description string    `json:"description"`
		Graph       string    `json:"graph"`
		CreatedAt   time.Time `json:"created_at,omitempty"`
	}
	if err := yaml.UnmarshalStrict(data, &legacy); err != nil {
		return AgentSpec{}, false, err
	}
	return AgentSpec{
		Card: agent.AgentCard{
			Name:        legacy.Name,
			Description: legacy.Description,
		},
		Engine: agent.EngineRef{
			Kind:     "agent.Engine",
			Impl:     "graph",
			Settings: graphSettings(legacy.Graph),
		},
		CreatedAt: legacy.CreatedAt,
	}, true, nil
}

// removeTimeout bounds UnregisterAgent drains from this package.
const removeTimeout = 30 * time.Second
