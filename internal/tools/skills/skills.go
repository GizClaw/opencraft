// Package skills provides the skill tools over the shared
// opencraft.skills registry: the model-facing counterpart of the
// per-turn ranked injection, so the model can browse the catalog,
// load full instructions, install new skills, and author its own
// (skill_create / skill_modify).
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
	// CreateName is the canonical skill_create tool name.
	CreateName = "skill_create"
	// ModifyName is the canonical skill_modify tool name.
	ModifyName = "skill_modify"

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
		createTool{t.svc},
		modifyTool{t.svc},
	}
}

type searchTool struct{ svc *skills.Service }

var _ tool.Tool = searchTool{}

func (searchTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		SearchName,
		"Searches the discovered skills catalog and returns "+
			"matching skill metadata (name, description, path). "+
			"Use skill_read to load a skill's full instructions, or "+
			"mention $<name> to activate it for this turn.",
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

type createTool struct{ svc *skills.Service }

var _ tool.Tool = createTool{}

func (createTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		CreateName,
		"Creates a new skill: writes a validated SKILL.md (frontmatter "+
			"name + description) into the repo or user skill root and "+
			"reloads the registry so the skill is usable immediately. "+
			"The body should be concise, self-contained Markdown "+
			"instructions an agent can follow without other context.",
		message.ToolProperty("name", "string",
			"Skill name: lowercase letters, digits and hyphens only."),
		message.ToolProperty("description", "string",
			"One-line description used by skill_search and per-turn ranking."),
		message.ToolProperty("body", "string",
			"The Markdown instructions injected when the skill is used."),
		message.ToolStringMapProperty("files",
			"Optional supporting files as a relative path -> content map, "+
				`e.g. {"scripts/run.py": "#!/usr/bin/env python3\n..."} or `+
				`{"scripts/validator/main.go": "..."}. Directories are created `+
				"as needed; paths must stay inside the skill directory."),
		message.ToolArrayProperty("executable",
			"Relative file paths to make executable (chmod 0755) after writing, "+
				"e.g. shell or Python entry scripts.",
			message.Items("string")),
		message.ToolPropertyWithDefault("scope", "string",
			`Where to create it: "repo" (default, <workspace>/.agents/skills) or "user" (~/.agents/skills).`, "repo"),
	).Required("name", "description", "body").Build()
}

func (createTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

func (t createTool) Execute(
	_ context.Context, arguments string,
) (string, error) {
	var args struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Body        string            `json:"body"`
		Scope       string            `json:"scope"`
		Files       map[string]string `json:"files"`
		Executable  []string          `json:"executable"`
	}
	if err := strictDecode(
		arguments, &args, "name", "description", "body", "scope", "files", "executable",
	); err != nil {
		return "", err
	}
	path, err := t.svc.Create(args.Name, skills.SkillDocument{
		Description: args.Description,
		Body:        args.Body,
		Files:       args.Files,
		Executable:  args.Executable,
	}, args.Scope)
	if err != nil {
		return "", err
	}
	return "Created skill " + args.Name + " at " + path +
		". The registry reloaded; it is now usable via skill_search.", nil
}

type modifyTool struct{ svc *skills.Service }

var _ tool.Tool = modifyTool{}

func (modifyTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		ModifyName,
		"Rewrites the SKILL.md of an existing skill (same name, full "+
			"new body), optionally updating its description, and "+
			"reloads the registry so the change takes effect "+
			"immediately. An empty description keeps the stored one.",
		message.ToolProperty("name", "string",
			"The skill name to modify."),
		message.ToolPropertyWithDefault("description", "string",
			"New one-line description; empty keeps the current one.", ""),
		message.ToolProperty("body", "string",
			"The full new Markdown instructions."),
		message.ToolStringMapProperty("files",
			"Optional supporting files to create or overwrite: relative path -> "+
				"content (e.g. python scripts, Go sources, references). Existing "+
				"files not listed are kept."),
		message.ToolArrayProperty("executable",
			"Relative file paths to make executable (chmod 0755), for new or "+
				"existing scripts.",
			message.Items("string")),
		message.ToolProperty("patch", "string",
			"Alternative to body/files: a codex-format apply_patch "+
				"(*** Begin Patch / *** Update File / *** Add File / "+
				"*** Delete File / *** End Patch) applied to the skill's "+
				"files, so you can change just a few lines of SKILL.md or "+
				"one script instead of rewriting the whole skill. Paths are "+
				"relative to the skill directory."),
		message.ToolPropertyWithDefault("scope", "string",
			`Which root the skill lives in: "repo" (default) or "user".`, "repo"),
	).Required("name").Build()
}

func (modifyTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

func (t modifyTool) Execute(
	_ context.Context, arguments string,
) (string, error) {
	var args struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Body        string            `json:"body"`
		Scope       string            `json:"scope"`
		Files       map[string]string `json:"files"`
		Executable  []string          `json:"executable"`
		Patch       string            `json:"patch"`
	}
	if err := strictDecode(
		arguments, &args, "name", "description", "body", "scope", "files", "executable", "patch",
	); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Patch) != "" {
		if strings.TrimSpace(args.Body) != "" ||
			len(args.Files) > 0 || len(args.Executable) > 0 ||
			strings.TrimSpace(args.Description) != "" {
			return "", errdefs.Validationf(
				"skill_modify: use either patch or body/files, not both")
		}
		paths, err := t.svc.Patch(args.Name, args.Patch, args.Scope)
		if err != nil {
			return "", err
		}
		return "Patched skill " + args.Name + ": " +
			strings.Join(paths, ", ") +
			". The registry reloaded; the changes are live.", nil
	}
	path, err := t.svc.Modify(args.Name, skills.SkillDocument{
		Description: args.Description,
		Body:        args.Body,
		Files:       args.Files,
		Executable:  args.Executable,
	}, args.Scope)
	if err != nil {
		return "", err
	}
	return "Updated skill " + args.Name + " at " + path +
		". The registry reloaded; skill_read and skill_search now see the new content.", nil
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
