// Package skills provides the skill_search and skill_read tools over
// the shared opencraft.skills registry: the model-facing counterpart
// of the per-turn ranked injection, so the model can browse the full
// catalog and load full instructions on demand.
package skills

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/skills"
)

const (
	// SearchName is the canonical skill_search tool name.
	SearchName = "skill_search"
	// ReadName is the canonical skill_read tool name.
	ReadName = "skill_read"
	// InstallName is the canonical skill_install tool name.
	InstallName = "skill_install"

	defaultSearchLimit = 10
)

// Tool bundles both skill tools over one registry.
type Tool struct {
	svc *skills.Service
}

// New creates the skill tools. svc is required.
func New(svc *skills.Service) (*Tool, error) {
	if svc == nil {
		return nil, errdefs.Validationf("skills tool: service is required")
	}
	return &Tool{svc: svc}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(svc *skills.Service) *Tool {
	t, err := New(svc)
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the skill_search and skill_read tools.
func (t *Tool) Tools() []tool.Tool {
	return []tool.Tool{
		searchTool{t.svc},
		readTool{t.svc},
		installTool{t.svc},
	}
}

type searchTool struct{ svc *skills.Service }

var _ tool.Tool = searchTool{}

func (searchTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		SearchName,
		"Searches the discovered skills catalog and returns "+
			"matching skill metadata (name, description, path). "+
			"Use skill_read to load a skill's full instructions, "+
			"or mention $<name> to activate it for this turn.",
		message.ToolProperty("query", "string",
			"Search text; empty lists the first skills."),
		message.ToolPropertyWithDefault("limit", "integer",
			"Maximum results to return.", defaultSearchLimit),
	).Build()
}

func (searchTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t searchTool) Execute(
	_ context.Context, arguments string,
) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := strictDecode(arguments, &args, "query", "limit"); err != nil {
		return "", err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	type hit struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Path        string  `json:"path"`
		Scope       string  `json:"scope,omitempty"`
		Score       float64 `json:"score,omitempty"`
	}
	var hits []hit
	if strings.TrimSpace(args.Query) == "" {
		list := t.svc.List()
		if len(list) > limit {
			list = list[:limit]
		}
		for _, sk := range list {
			hits = append(hits, hit{
				Name:        sk.Name,
				Description: sk.Description,
				Path:        sk.Path,
				Scope:       sk.Scope,
			})
		}
	} else {
		for _, sc := range t.svc.RankScored(args.Query, limit, t.svc.MinScore()) {
			hits = append(hits, hit{
				Name:        sc.Skill.Name,
				Description: sc.Skill.Description,
				Path:        sc.Skill.Path,
				Scope:       sc.Skill.Scope,
				Score:       sc.Score,
			})
		}
	}
	data, err := json.Marshal(hits)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type readTool struct{ svc *skills.Service }

var _ tool.Tool = readTool{}

func (readTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		ReadName,
		"Loads the full SKILL.md instructions of one discovered skill.",
		message.ToolProperty("name", "string",
			"The skill name (as listed by skill_search or the "+
				"per-turn skills section)."),
	).Required("name").Build()
}

func (readTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t readTool) Execute(
	_ context.Context, arguments string,
) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictDecode(arguments, &args, "name"); err != nil {
		return "", err
	}
	sk, body, err := t.svc.ReadFull(args.Name)
	if err != nil {
		return "", err
	}
	return "# Skill: " + sk.Name + " (file: " + sk.Path + ")\n\n" + body, nil
}

type installTool struct{ svc *skills.Service }

var _ tool.Tool = installTool{}

func (installTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		InstallName,
		"Installs a skill from a git repository into the user or "+
			"repo skill root, validates its SKILL.md, and reloads the "+
			"registry so it is usable immediately (no restart). Use "+
			"path when the skill lives in a subdirectory of the repo.",
		message.ToolProperty("repo", "string",
			"Git repository URL or local path."),
		message.ToolProperty("path", "string",
			`Subdirectory inside the repo containing the skill (e.g. "skills/flowcraft-config"). Empty installs the whole repo.`),
		message.ToolPropertyWithDefault("scope", "string",
			`Target scope: "user" (default, ~/.agents/skills) or "repo" (.agents/skills).`, "user"),
	).Required("repo").Build()
}

func (installTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

func (t installTool) Execute(
	_ context.Context, arguments string,
) (string, error) {
	var args struct {
		Repo  string `json:"repo"`
		Scope string `json:"scope"`
		Path  string `json:"path"`
	}
	if err := strictDecode(arguments, &args, "repo", "scope", "path"); err != nil {
		return "", err
	}
	dst, err := t.svc.Install(args.Repo, args.Scope, args.Path)
	if err != nil {
		return "", err
	}
	return "Installed skill at " + dst +
		". The registry reloaded; verify with /skills or skill_search.", nil
}

// strictDecode parses tool arguments and rejects unknown fields.
func strictDecode(arguments string, v any, allowed ...string) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &top); err != nil {
		return errdefs.Validationf("parse arguments: %v", err)
	}
	for key := range top {
		if !slices.Contains(allowed, key) {
			return errdefs.Validationf("unknown argument %q", key)
		}
	}
	if err := json.Unmarshal([]byte(arguments), v); err != nil {
		return errdefs.Validationf("parse arguments: %v", err)
	}
	return nil
}
