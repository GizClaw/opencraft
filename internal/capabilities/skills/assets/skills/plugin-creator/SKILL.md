---
name: plugin-creator
description: Build an OpenCraft plugin (plugin.json manifest plus a UI bundle, skills, MCP servers, hooks, or a capability subprocess) inside the workspace and install it into the running app with plugin_install; use when the user asks to extend OpenCraft itself with a panel, command, tool, skill, hook, or provider.
---

# OpenCraft Plugin Creator

A plugin is a directory the app loads at runtime: a `plugin.json`
manifest, an ES module entry bundle, and any agent-facing capability
(skills, MCP servers, hooks, tools) the manifest declares. The
reference implementation is `plugins/hello` in the OpenCraft repo
(`plugin.json`, `dist/index.js`, `skills/hello/SKILL.md`,
`plugins/hello/README.md`).

## Where the code goes

Write the plugin **inside the workspace** — that is the only place the
session sandbox can write:

```text
<workspace>/.opencraft-plugins/<plugin-id>/plugin.json
<workspace>/.opencraft-plugins/<plugin-id>/dist/index.js
```

You cannot write `~/.opencraft/plugins` yourself: the registry is
outside the session sandbox, and installation is a host-side copy
triggered by `plugin_install`, which the user confirms. A session the
user switched to YOLO can technically reach that directory — do not use
it as an installation path: only `plugin_install` / `plugin_update` show
the user what is about to run and reload the running runtime.

## Manifest

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "0.1.0",
  "minHostVersion": "0.5.0",
  "entry": "dist/index.js",
  "permissions": ["storage:kv", "commands:register", "statusbar:contribute"],
  "contributes": {
    "settingsPanels": [{ "id": "my-panel", "title": "My", "order": 10 }],
    "sidebarEntries": [{ "id": "my-entry", "title": "My", "order": 10 }]
  }
}
```

- `id` matches `^[a-z0-9][a-z0-9._-]{0,63}$`; `name`, `version` and
  `entry` are required. Versions are dotted numerics with an optional
  `-prerelease` (`1.2.0`, `1.2.0-beta.1`).
- `permissions` is a closed set; an unknown entry rejects the whole
  plugin: `secrets:auth`, `storage:kv`, `events:subscribe`,
  `commands:register`, `statusbar:contribute`, `pets:contribute`,
  `tools:expose`, `sessions:import`, `skills:contribute`,
  `mcp:contribute`, `hooks:register`, `telemetry:export`. Declare only
  what you use — the settings page shows them to the user, and the
  agent capabilities below are refused without them.
- `contributes.settingsPanels` / `contributes.sidebarEntries` declare
  the ids the bundle registers; the detail drawer lists them.

## UI bundle (`entry`)

A plain ES module — no build step needed. The host imports it by URL
and mounts it as a Cordis plugin:

```js
export const name = 'my-plugin';
export const inject = ['storage', 'react'];

export function apply(ctx) {
  const React = ctx.react; // host React runtime; JSX is NOT available
  ctx.settingsPanels.add({ id: 'my-panel', title: 'My', order: 10, Component: Panel });
  ctx.commands.add({ id: 'my-cmd', title: 'Do it', run: () => ctx.ui.flash('done') });
  ctx.on('turn_end', () => {});       // host UI events
  ctx.effect(() => { /* cleanup */ }); // reversible side effect
}
```

- Always available: `ctx.react`, `ctx.ui` (`flash(text)`),
  `ctx.host.version`, `ctx.pets`, and the registrars
  `ctx.settingsPanels` / `ctx.sidebarEntries` / `ctx.commands` /
  `ctx.statusBar` (`add()` returns a disposer tied to the plugin
  scope), plus `ctx.on` / `ctx.effect`.
- Permission-gated services: `ctx.storage` (KV get/set/delete/list;
  needs `storage:kv`) and `ctx.secrets` (needs `secrets:auth`). Asking
  for a service in `inject` without its permission fails activation.
- Everything registered through the context is torn down when the
  plugin is disabled or reloaded.

## Agent-facing capabilities

Each group requires its manifest permission and is ignored without it:

- `"skills": ["skills"]` (`skills:contribute`) — skill roots added to
  the shared skills registry (a plain `skills/` directory works too).
- `"mcpServers": [{"name": "x", "transport": "stdio", "command": "bin/server", "args": [], "env": {}}]`
  (`mcp:contribute`) — stdio (plugin-relative paths are resolved) or
  `{"transport": "http", "url": "https://…"}`; every tool arrives
  namespaced by plugin and server.
- `"hooks": ["hooks/hooks.json"]` (`hooks:register`) — command hooks run
  with the plugin directory as cwd; payload fields that carry content
  (prompts, tool input/results) are stripped first, because plugin
  hooks are an untrusted source.
- `"tools": [{"name": "ping", "description": "…", "method": "ping", "inputSchema": {"type": "object"}}]`
  (`tools:expose`) — requires `capability`; the agent sees each one as
  `<plugin_id>__<name>`.

Bounds: at most 64 tools, 32 skills, 16 hooks, 16 MCP servers, 8 pets;
tool descriptions up to 1024 characters, input schemas up to 32 KiB,
manifest up to 1 MiB.

## Capability subprocess

```json
"capability": { "binary": "bin/my-plugin", "protocol": 1, "hosts": ["api.example.com"] }
```

is a native program the host runs per plugin id and drives over
line-delimited JSON-RPC 2.0 on stdin/stdout. It must first send

```json
{"jsonrpc":"2.0","id":1,"method":"handshake","params":{"id":"my-plugin","protocol":1}}
```

and then answer host→plugin method calls. It may call host primitives
back, each gated by a manifest permission: `secret.get/set/delete`,
`open.url` (only the hosts listed in `capability.hosts`),
`inference.upsert/remove`, `session.import` /
`session.imported_sources`, `workspace.current`,
`telemetry.configure/disable` (`emit.event` is reserved and currently
a no-op).
The declared `capability.protocol` must equal the host's protocol
version (1), and the handshake re-checks it. Everything the child writes
to stderr is forwarded to the app log — never log credentials. On macOS
the host ad-hoc signs the binary during install, so ship an unsigned
build.

## Install and iterate

1. Write the files in the workspace (`apply_patch`, `write_file`, or
   `exec_command` for a build).
2. `plugin_install({ "path": ".opencraft-plugins/my-plugin" })` — the
   user sees the id, version, permissions, entry bundle and capability
   binary, then confirms. The plugin is enabled on install, and the
   runtime reloads once the current turn ends, so its skills, tools,
   MCP servers and hooks are live from the next turn on.
3. Iterate with `plugin_update({ "id": "my-plugin", "path": "…" })`:
   the manifest must keep the id and carry a strictly newer version.
   The previous version stays as a rollback snapshot, and enabled
   state, KV data, secrets and inference profiles survive.
4. `plugin_list()` shows the registry. Agent installs and user installs
   share one registry; the user uninstalls, rolls back or disables the
   plugin in Settings → Plugins.

A `.zip` package works too (`plugin_install` accepts a `.zip` path):
`plugin.json` may sit at the archive root or inside a single top-level
directory — handy when the user prefers one artifact over a folder.
