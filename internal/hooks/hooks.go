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

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: ResourceImpl}
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
	return Load(settings.Path)
}

// Manager owns the loaded hook groups.
type Manager struct {
	path   string
	groups map[string][]groupEntry
}

type groupEntry struct {
	re    *regexp.Regexp
	hooks []Hook
}

// Load parses hooks.json at path. A missing file returns an empty
// manager. Invalid groups abort loading so misconfiguration is loud.
func Load(path string) (*Manager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Manager{path: path, groups: map[string][]groupEntry{}}, nil
		}
		return nil, err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, errdefs.Validationf(
			"opencraft hooks: parse %s: %v", path, err)
	}
	m := &Manager{path: path, groups: map[string][]groupEntry{}}
	for event, groups := range cfg.Hooks {
		for i, g := range groups {
			var re *regexp.Regexp
			if strings.TrimSpace(g.Matcher) != "" &&
				strings.TrimSpace(g.Matcher) != "*" {
				compiled, err := regexp.Compile(g.Matcher)
				if err != nil {
					return nil, errdefs.Validationf(
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
					return nil, errdefs.Validationf(
						"opencraft hooks: %s[%d] hook command is required", event, i)
				}
				hooks = append(hooks, h)
			}
			if len(hooks) > 0 {
				m.groups[event] = append(m.groups[event], groupEntry{re: re, hooks: hooks})
			}
		}
	}
	return m, nil
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
			m.run(ctx, event, h, payload)
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
	payload map[string]any,
) {
	timeout := defaultTimeout
	if h.Timeout > 0 {
		timeout = time.Duration(h.Timeout) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	data, err := json.Marshal(payload)
	if err != nil {
		telemetry.Warn(ctx, "opencraft hooks: marshal event failed",
			otellog.String("event", event),
			otellog.String("error", err.Error()))
		return
	}
	cmd := exec.CommandContext(runCtx, "sh", "-c", h.Command)
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
