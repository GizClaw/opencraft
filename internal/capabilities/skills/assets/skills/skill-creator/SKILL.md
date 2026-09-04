---
name: skill-creator
description: Create a new skill (SKILL.md plus supporting files) when the user asks to create, scaffold, build, or add files to a skill; covers scope choice, structure, and verification.
---

# Skill Creator

Skills are directories with a `SKILL.md` frontmatter, discovered from
named roots. The built-in skills are embedded in the binary and
read-only.

## Choose the scope

- **repo** — `<workspace>/.agents/skills/<name>/`: inside the workspace,
  create/edit directly with write_file / apply_patch / exec.
- **user** — `~/.agents/skills/<name>/`: outside the workspace; writing
  it requires the session to be in YOLO mode (the user switches with
  `/permissions` and confirms). Ask the user to switch if the session is
  not already in YOLO mode.

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

- The registry is scanned at process start: a newly created skill is
  discoverable via skill_search and the per-turn skills section after the
  next restart (skill_install is the only tool that reloads immediately,
  and it installs from git).
- Verify by reading back SKILL.md and checking the frontmatter
  (name + description).
