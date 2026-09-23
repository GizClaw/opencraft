package worldstate

import (
	"context"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
)

// userMemorySection renders the user-level long-term memory: facts the
// user (or the remember tool) asked to keep across sessions. The
// section is re-read every turn like AGENTS.md — a fact accepted in the
// settings page or written by the tool is visible on the next turn
// without a rebuild — and it rides the stable-first user context (right
// after AGENTS.md) rather than the per-turn tail: facts change on an
// explicit user action, not once per turn, so the prefix stays
// cacheable in practice.
//
// Two bounds apply, both from the usermemory resource settings: how
// many facts are considered, and how many bytes of them are injected.
// The newest facts win when the byte budget runs out, and the count of
// the ones that did not fit is reported so the model knows the section
// is partial.
func (s *Service) userMemorySection(ctx context.Context) (Section, error) {
	binding := s.opts.UserMemory
	if !binding.Enabled() {
		return Section{}, nil
	}
	cfg := binding.Config
	facts, err := binding.Memory.List(ctx, userstore.Query{
		Workspace: s.opts.WorkBase,
		Limit:     cfg.InjectMaxItems,
	})
	if err != nil {
		return Section{}, fmt.Errorf("worldstate: list user memory: %w", err)
	}
	if len(facts) == 0 {
		return Section{}, nil
	}

	// The byte budget bounds the fact list, not the fixed header above
	// it. A fact that does not fit is skipped rather than truncated (a
	// half sentence injected into every later turn is worse than an
	// omission) and a later, shorter fact may still fit.
	kept := make([]string, 0, len(facts))
	used := 0
	for _, fact := range facts {
		text := strings.TrimSpace(fact.Text)
		if text == "" {
			continue
		}
		if used+len(text)+len("- \n") > cfg.InjectMaxChars {
			continue
		}
		kept = append(kept, text)
		used += len(text) + len("- \n")
	}
	if len(kept) == 0 {
		// Nothing fits (or there is nothing to say): the header alone
		// would only tell the model that memory exists.
		return Section{}, nil
	}
	rendered, err := render(userMemoryTmpl, userMemoryData{
		Facts:   kept,
		Omitted: len(facts) - len(kept),
	})
	if err != nil {
		return Section{}, err
	}
	telemetry.Info(ctx, "worldstate: user memory injected",
		log.Int("facts", len(kept)),
		log.Int("omitted", len(facts)-len(kept)),
		log.Int("bytes", len(rendered)),
		log.Int("max_chars", cfg.InjectMaxChars))
	return newTextSection(
		"user_memory", message.RoleUser, strings.TrimRight(rendered, "\n")), nil
}

// userMemoryData is the view for user_memory.gotmpl.
type userMemoryData struct {
	Facts   []string
	Omitted int
}
