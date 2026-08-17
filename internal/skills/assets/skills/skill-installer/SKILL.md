---
name: skill-installer
description: Install skills from a git repository; use when the user asks to install, add, fetch, or set up a new skill.
---

# Skill Installer

When the user asks to install a skill:

1. Determine the source — a git repository URL is best — and the target scope: "user" for personal skills (`~/.agents/skills`) or "repo" for project skills (`.agents/skills` at the workspace root).
2. If the skill lives in a subdirectory of the repository (e.g. `skills/flowcraft-config`), pass that as `path` so only that directory is installed.
3. Call the `skill_install` tool with `repo`, `path` (when needed) and `scope`.
4. Report the installed path and confirm the skill is now discoverable (`/skills` or `skill_search`).

There is no curated registry yet: if no URL is given, ask the user for one.
