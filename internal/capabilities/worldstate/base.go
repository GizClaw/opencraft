package worldstate

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/log"
)

// fragment is one model-facing instruction fragment. Files are plain
// markdown embedded in the binary. There is intentionally no user-dir
// override layer yet; if one returns, it must go through the same
// budget and validation guards as the embedded files.
type fragment struct {
	ID   string
	File string
}

// baseFragmentOrder is the fixed model-visible order for always-on
// instructions. Keep fragments small and single-purpose; optional
// mode/personality fragments render after them, and the per-turn
// session/context sections render last.
var baseFragmentOrder = []fragment{
	{ID: "base_identity", File: "base/identity.md"},
	{ID: "base_work", File: "base/work.md"},
	{ID: "base_sandbox", File: "base/sandbox.md"},
	{ID: "base_editing", File: "base/editing.md"},
	{ID: "base_agents", File: "base/agents.md"},
	{ID: "base_special", File: "base/special.md"},
	{ID: "base_format", File: "base/format.md"},
}

// modeFragments maps a collaboration mode (worldstate option value)
// to its instruction fragment. "default" intentionally has no
// fragment: the base instructions already describe default behavior.
var modeFragments = map[string]fragment{
	"plan": {ID: "mode_plan", File: "modes/plan.md"},
}

// personalityFragments maps a personality name (worldstate option)
// to its instruction fragment. The slot stays empty when no
// personality is configured.
var personalityFragments = map[string]fragment{
	"friendly":  {ID: "personality_friendly", File: "personality/friendly.md"},
	"pragmatic": {ID: "personality_pragmatic", File: "personality/pragmatic.md"},
}

const (
	// promptFragmentMaxBytes caps a single embedded fragment. The cap
	// keeps one authoring mistake from silently bloating the model
	// window.
	promptFragmentMaxBytes = 6 << 10
	// promptInstructionTotalBudget bounds the always-on base layer plus
	// any optional mode/personality fragments active in one session
	// (currently base ~8.9 KiB + plan ~2.3 KiB + largest personality
	// ~0.7 KiB). It is a rough authoring guard, not a token estimate;
	// compact.js accounts the actual world-section size per turn.
	promptInstructionTotalBudget = 16 << 10
)

// instructionSections renders base fragments plus the optional mode
// and personality fragments in fixed order. ctx is retained so
// diagnostics (telemetry warnings for unknown/missing fragments) and
// future per-turn state (for example a session-scoped mode) can thread
// context without changing the call site again.
func (s *Service) instructionSections(ctx context.Context) []Section {
	out := make([]Section, 0, len(baseFragmentOrder)+2)
	for _, spec := range instructionGroupOrder {
		if secs := spec.Render(ctx, s); len(secs) > 0 {
			out = append(out, secs...)
		}
	}
	return out
}

func renderFragments(ctx context.Context, s *Service, specs []fragment) []Section {
	out := make([]Section, 0, len(specs))
	for _, spec := range specs {
		text, ok := s.readFragment(ctx, spec.File)
		if !ok {
			continue
		}
		out = append(out, newTextSection(spec.ID, message.RoleSystem, text))
	}
	return out
}

// modeSections injects the collaboration-mode fragment when the
// session runs in a non-default mode. Unknown modes are ignored so a
// future mode added to the harness degrades to default behavior
// rather than erroring the turn; the mismatch is logged so a config
// typo does not fail silently.
func (s *Service) modeSections(ctx context.Context) []Section {
	mode := strings.ToLower(strings.TrimSpace(s.opts.CollaborationMode))
	if mode == "" || mode == "default" {
		return nil
	}
	spec, ok := modeFragments[mode]
	if !ok {
		telemetry.Warn(ctx, "worldstate: unknown collaboration mode, using default instructions",
			log.String("mode", mode))
		return nil
	}
	return renderFragments(ctx, s, []fragment{spec})
}

// personalitySections injects the personality fragment when one is
// configured. Missing personalities are ignored (neutral default), and
// unknown names are logged so a config typo is visible.
func (s *Service) personalitySections(ctx context.Context) []Section {
	name := strings.ToLower(strings.TrimSpace(s.opts.Personality))
	if name == "" {
		return nil
	}
	spec, ok := personalityFragments[name]
	if !ok {
		telemetry.Warn(ctx, "worldstate: unknown personality, using neutral default",
			log.String("personality", name))
		return nil
	}
	return renderFragments(ctx, s, []fragment{spec})
}

// readFragment returns one embedded fragment's text. A missing,
// oversized, or empty fragment is skipped (returns not-ok) with a
// telemetry warning rather than failing the turn, so an authoring
// mistake in the embedded assets is visible instead of silently
// removing a model-facing section.
func (s *Service) readFragment(ctx context.Context, rel string) (string, bool) {
	data, err := templateFS.ReadFile("templates/" + rel)
	if err != nil {
		telemetry.Warn(ctx, "worldstate: instruction fragment missing",
			log.String("fragment.file", rel))
		return "", false
	}
	if len(data) > promptFragmentMaxBytes {
		telemetry.Warn(ctx, "worldstate: instruction fragment exceeds size budget",
			log.String("fragment.file", rel),
			log.Int("bytes", len(data)),
			log.Int("budget_bytes", promptFragmentMaxBytes))
		return "", false
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		telemetry.Warn(ctx, "worldstate: instruction fragment is empty",
			log.String("fragment.file", rel))
		return "", false
	}
	return text, true
}
