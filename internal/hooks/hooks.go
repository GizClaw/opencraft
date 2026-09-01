// Package hooks implements opencraft's external lifecycle hooks: a
// user-configured hooks.json whose command hooks fire on agent-loop
// events (PreToolUse / PostToolUse / UserPromptSubmit /
// PermissionRequest / TurnEnd). Each matching command receives one JSON
// event object on stdin. Hook failures never break
// the agent loop: they are logged through telemetry and skipped.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// Event names. Matcher (when non-empty) is a regex over the event's
// "tool" field for tool events; other events run only matcher-less
// groups.
const (
	EventPreToolUse        = "PreToolUse"
	EventPostToolUse       = "PostToolUse"
	EventUserPromptSubmit  = "UserPromptSubmit"
	EventPermissionRequest = "PermissionRequest"
	EventTurnEnd           = "TurnEnd"
	EventSessionStart      = "SessionStart"
	EventSessionEnd        = "SessionEnd"
	EventSubagentStart     = "SubagentStart"
	EventSubagentStop      = "SubagentStop"
)

// defaultTimeout caps a hook run when its config omits timeout.
const defaultTimeout = 30 * time.Second

// maxHookOutput bounds the captured stdout/stderr retained for logging.
const maxHookOutput = 64 << 10

// Hook is one command invocation.
type Hook struct {
	Type    string `json:"type"` // "command" (v1); others are skipped
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // seconds; 0 = default
}

// Group filters one event by matcher and runs its hooks.
type Group struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []Hook `json:"hooks"`
}

// ExtraSource is one additional hooks.json file (normally
// plugin-contributed). Dir anchors relative hook commands to the
// plugin directory. Trusted=false marks third-party sources whose
// payload is sanitized before the command runs (tool inputs, tool
// results, prompts and command text are stripped).
type ExtraSource struct {
	Path    string
	Dir     string
	Trusted bool
}

// configFile is the on-disk hooks.json shape.
type configFile struct {
	Hooks map[string][]Group `json:"hooks"`
}

// ResourceKind is the deploy resource kind.
const ResourceKind = "opencraft.hooks"

// ResourceImpl is the deploy impl id.
const ResourceImpl = "local"

// Settings is the deploy-document shape: the hooks.json path
// (env-expanded).
type Settings struct {
	Path string `json:"path"`
}

// Factory builds the opencraft.hooks resource.
type Factory struct{}

var _ resource.Factory = Factory{}

// pluginHooksProvider is implemented by the shared plugin host
// (internal/plugins/agent) and contributes plugin hook files.
type pluginHooksProvider interface {
	PluginHooks() []ExtraSource
}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "plugin.host", Type: "opencraft.plugins", Required: false},
		},
	}
}

// New implements resource.Factory. A missing hooks.json yields an empty
// (no-op) manager, not an error.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft hooks: decode settings: %v", err)
	}
	var extra []ExtraSource
	if dep, ok := in.Dep("plugin.host"); ok {
		if p, ok := dep.(pluginHooksProvider); ok && p != nil {
			extra = append(extra, p.PluginHooks()...)
		}
	}
	return LoadWithSources(settings.Path, extra)
}

// Manager owns the loaded hook groups.
type Manager struct {
	path   string
	groups map[string][]groupEntry
}

type groupEntry struct {
	re      *regexp.Regexp
	hooks   []Hook
	dir     string
	trusted bool
}

// Load parses hooks.json at path. A missing file returns an empty
// manager. Invalid groups abort loading so misconfiguration is loud.
func Load(path string) (*Manager, error) {
	return LoadWithSources(path, nil)
}

// LoadWithSources parses the user hooks.json plus any plugin-provided
// hook files. A missing user file is fine; a missing plugin file is an
// error so a broken plugin never silently loses its hooks.
func LoadWithSources(path string, extra []ExtraSource) (*Manager, error) {
	m := &Manager{path: path, groups: map[string][]groupEntry{}}
	if err := m.loadFile(path, "", true); err != nil {
		return nil, err
	}
	for _, src := range extra {
		if err := m.loadFile(src.Path, src.Dir, src.Trusted); err != nil {
			// A broken plugin hook must not take down the whole
			// runtime: skip the source and surface the failure through
			// telemetry, matching the registry's per-plugin error model.
			telemetry.Warn(context.Background(),
				"opencraft hooks: skipping plugin hook source",
				otellog.String("path", src.Path),
				otellog.String("error", err.Error()))
		}
	}
	return m, nil
}

func (m *Manager) loadFile(path, dir string, trusted bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && dir == "" {
			return nil
		}
		return err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return errdefs.Validationf(
			"opencraft hooks: parse %s: %v", path, err)
	}
	for event, groups := range cfg.Hooks {
		for i, g := range groups {
			var re *regexp.Regexp
			if strings.TrimSpace(g.Matcher) != "" &&
				strings.TrimSpace(g.Matcher) != "*" {
				compiled, err := regexp.Compile(g.Matcher)
				if err != nil {
					return errdefs.Validationf(
						"opencraft hooks: %s[%d].matcher: %v", event, i, err)
				}
				re = compiled
			}
			var hooks []Hook
			for _, h := range g.Hooks {
				if h.Type != "" && h.Type != "command" {
					continue // v1 supports command hooks only
				}
				if strings.TrimSpace(h.Command) == "" {
					return errdefs.Validationf(
						"opencraft hooks: %s[%d] hook command is required", event, i)
				}
				hooks = append(hooks, h)
			}
			if len(hooks) > 0 {
				m.groups[event] = append(m.groups[event], groupEntry{
					re: re, hooks: hooks, dir: dir, trusted: trusted,
				})
			}
		}
	}
	return nil
}

// Path returns the hooks.json path.
func (m *Manager) Path() string { return m.path }

// Empty reports whether the manager has no groups.
func (m *Manager) Empty() bool { return len(m.groups) == 0 }

// Fire runs every group matching event/payload. It never returns an
// error: hook failures are telemetry-logged and skipped so the agent
// loop is never blocked by user hooks.
func (m *Manager) Fire(ctx context.Context, event string, payload map[string]any) {
	groups := m.groups[event]
	if len(groups) == 0 {
		return
	}
	for _, g := range groups {
		if g.re != nil && !g.re.MatchString(matchValue(payload)) {
			continue
		}
		for _, h := range g.hooks {
			m.run(ctx, event, h, g.dir, g.trusted, payload)
		}
	}
}

// matchValue returns the field the matcher regex is tested against:
// tool events use "tool", session events "source"/"reason", subagent
// events "subagent".
func matchValue(payload map[string]any) string {
	for _, key := range []string{"tool", "source", "reason", "subagent"} {
		if v, ok := payload[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// run executes one command hook with the event payload on stdin.
func (m *Manager) run(
	ctx context.Context,
	event string,
	h Hook,
	dir string,
	trusted bool,
	payload map[string]any,
) {
	timeout := defaultTimeout
	if h.Timeout > 0 {
		timeout = time.Duration(h.Timeout) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runPayload := payload
	if !trusted {
		runPayload = sanitizePayload(payload)
	}
	data, err := json.Marshal(runPayload)
	if err != nil {
		telemetry.Warn(ctx, "opencraft hooks: marshal event failed",
			otellog.String("event", event),
			otellog.String("error", err.Error()))
		return
	}
	cmd := exec.CommandContext(runCtx, "sh", "-c", h.Command)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		output := out.String()
		if len(output) > maxHookOutput {
			output = output[:maxHookOutput]
		}
		telemetry.Warn(ctx, "opencraft hooks: command hook failed",
			otellog.String("event", event),
			otellog.String("command", h.Command),
			otellog.String("error", err.Error()),
			otellog.String("output", output))
	}
}

// sanitizePayload strips content-bearing fields before a third-party
// (plugin) hook command sees them. Identifiers and status fields stay;
// tool inputs, tool results, prompts, command text, errors and
// subagent messages are removed so untrusted hooks cannot exfiltrate
// raw conversation or tool data.
func sanitizePayload(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	for _, key := range []string{
		"tool_input",
		"tool_result",
		"prompt",
		"command",
		"error",
		"message",
		"target",
	} {
		delete(out, key)
	}
	return out
}
