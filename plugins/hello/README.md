# Hello Plugin

The reference plugin for the OpenCraft plugin host (Phase 0). It
contributes one settings panel, one sidebar entry, one command and one
status-bar item; clicking the sidebar entry flashes a greeting.

## Install

```sh
mkdir -p ~/.opencraft/plugins
cp -R plugins/hello ~/.opencraft/plugins/hello
```

Then open OpenCraft → Settings → Plugins and press Refresh, or restart
the app. The plugin is enabled by default; disable it from the same
page.

## Bundle contract

The protocol is Cordis. `dist/index.js` is an ES module; the host
assembles it into a Cordis plugin (`{ name, inject, apply }`) and
mounts it with `ctx.plugin()`:

```js
export const name = 'hello';
export const inject = ['storage', 'react'];

export function apply(ctx) {
  // ctx.react              host React runtime for building components
  // ctx.ui.flash(text)     transient status message (always available)
  // ctx.storage            KV service (needs "storage:kv")
  // ctx.settingsPanels.add / ctx.sidebarEntries.add / ctx.commands.add /
  // ctx.statusBar.add      contribution registrars (return disposers)
  // ctx.on(type, handler)  typed host UI events (always available)
  // ctx.effect(fn)         reversible side effect
}
```

Every registration is a reversible effect tied to the plugin's Cordis
scope: contributions, `ctx.on` subscriptions and `ctx.effect` cleanups
all run in reverse order when the plugin is disabled or reloaded.
See `docs/plans/plugin-system.md` for the full design.

## Agent-facing capabilities

Since the Phase 2 extension, a plugin may also contribute capabilities
directly to the agent runtime. Each group requires its manifest
permission and fails closed without it:

- `skills:contribute` — `skills` paths (or a default `<root>/skills`
  directory) are registered into the shared skills registry.
- `mcp:contribute` — `mcpServers` are attached through the same MCP
  source the settings page uses; stdio commands resolve relative to the
  plugin directory when they contain a path separator (bare PATH
  commands like `npx` are left untouched), and every tool is
  namespaced by plugin + server.
- `hooks:register` — `hooks` paths point at hooks.json files; commands
  run with the plugin directory as cwd. Plugin hooks are untrusted
  sources: content-bearing payload fields (`tool_input`, `tool_result`,
  `prompt`, `command`, errors and subagent messages) are stripped
  before the command runs.
- `tools:expose` — `tools` declare capability subprocess methods that
  become ordinary agent tools (`<plugin>:<tool>`); they require a
  `capability` binary.

The hello plugin demonstrates the skills side: `skills/hello/SKILL.md`
is discovered as a normal skill when the plugin is enabled.

## Update and rollback

Installed user plugins can be updated from a folder or zip through the
plugin manager. Update enforces:

- the new manifest `id` must match the installed plugin;
- the new `version` must be strictly newer than the installed one
  (semver-style ordering: dotted numeric core with optional
  `-prerelease`, where releases sort above prereleases);
- `minHostVersion` must not exceed the running host version when the
  host records one.

The previous version is snapshotted to `<root>/.backups/<id>` before
the swap, so a failed replace restores it automatically and the UI
offers an explicit rollback afterwards. Enabled state, KV data,
secrets and inference profiles survive update and rollback; builtin
plugins cannot be updated or rolled back.

## Remote update checks (update.url)

Plugins may declare a remote update manifest:

```json
{
  "update": {
    "url": "https://example.com/plugins/hello/latest.json"
  }
}
```

The endpoint must return:

```json
{
  "version": "0.2.0",
  "download_url": "https://example.com/plugins/hello-0.2.0.zip",
  "checksum": "sha256:<64 hex chars>",
  "changelog": "What changed"
}
```

The host validates the URL (https, no credentials, SSRF guard), the
remote version, and the sha256 checksum before downloading; the
downloaded zip then goes through the normal `UpdateZip` pipeline with
version constraints and rollback. `changelog` is optional.
