---
name: hello
description: The hello reference plugin's skill; demonstrates that a plugin can contribute Agent Skills alongside its UI contributions.
---

# Hello Plugin Skill

This skill ships with the reference hello plugin. It exists to show the
plugin contract extension: a plugin can contribute `skills` directories
next to its Cordis UI bundle, and the opencraft agent discovers them
through the shared skills registry (`skill_search` / `skill_read`).

When the plugin is enabled, this skill is available without any
additional installation step.
