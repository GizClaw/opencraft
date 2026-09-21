---
name: skill-creator
description: Create a new skill (SKILL.md plus supporting files) when the user asks to create, scaffold, build, or add files to a skill; covers structure and verification.
---

# Skill Creator

Skills are directories with a `SKILL.md` frontmatter, discovered from
named roots. The built-in skills are embedded in the binary and
read-only.

## Where skills live

The user skill root is `~/.agents/skills/<name>/`. The `skill_create` /
`skill_modify` tools write there directly (host-side). Editing the
files by hand through the sandbox only works in YOLO mode (the user
switches with `/permissions` and confirms); prefer the tools.

## Structure

Create `<root>/<name>/SKILL.md` with frontmatter:

```markdown
---
name: <kebab-case>
description: one or two sentences on when to use this skill
---

<body: concise steps and rules; keep background in references>
```

Add supporting files as needed:
- `references/*.md` — details loaded on demand
- `scripts/*` — executable helpers; set the executable bit, e.g. via
  exec_command `chmod +x scripts/tool.py`
- `assets/*` — non-markdown resources

## Rules

- name: kebab-case, unique across skill roots, no spaces.
- description: say when the user would want this skill so ranking finds it.
- Keep the body actionable; prefer references for background material.
- Never write into the built-in skill area (it is embedded and read-only).

## After creating

- Creating or editing a skill through the skill tools reloads the
  registry: the change is discoverable via skill_search and the
  per-turn skills section immediately.
- A SKILL.md edited directly on disk is picked up on the next registry
  reload (a runtime rebuild), not instantly.
- Verify by reading back SKILL.md and checking the frontmatter
  (name + description).
