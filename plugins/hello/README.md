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
export const name = "hello";
export const inject = ["storage", "react"];

export function apply(ctx) {
  // ctx.react              host React runtime for building components
  // ctx.ui.flash(text)     transient status message (always available)
  // ctx.host.version       host app version (always available, read-only)
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
  become ordinary agent tools (`<plugin>__<tool>`); they require a
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
- when overriding a builtin, the new version must not be older than
  the builtin version;
- `minHostVersion` must not exceed the running host version when the
  host records one.

The previous version is snapshotted to `<root>/.backups/<id>` before
the swap, so a failed replace restores it automatically and the UI
offers an explicit rollback afterwards. Enabled state, KV data,
secrets and inference profiles survive update and rollback.

## Builtin plugins and overrides

App-bundled (builtin) plugins are read-only: they cannot be rolled back
or uninstalled in place, only disabled. To override a builtin, install
a user plugin with the same `id` (a folder, zip, or remote update
package) into `~/.opencraft/plugins/<id>/`. The user copy then
**shadows** the builtin and behaves like any other user plugin: it can
be updated, rolled back, and uninstalled. Uninstalling the shadow
removes only the user copy and reveals the builtin again.

The shadow's version must be at least the builtin's version (equal is
allowed); older installs and updates are rejected. The install dialog
warns before installing a shadow, and the plugin list marks overrides
with a badge. Enable state is shared per id. A builtin that declares
`update.url` can also be updated directly: applying the update installs
the package as a user shadow, and uninstalling it restores the builtin.

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

## Installing from the agent

The agent can build a plugin inside its workspace and install it
without leaving the conversation. The registry lives under
`~/.opencraft/plugins`, outside the session sandbox, so the copy is a
host-side step: three tools drive it, and each mutation is confirmed by
the user first.

- `plugin_install({ "path": ".opencraft-plugins/hello" })` — installs a
  directory containing `plugin.json` or a `.zip` package that the agent
  wrote in the workspace. The confirmation shows the id, version,
  permissions, entry bundle and capability binary before anything is
  copied.
- `plugin_update({ "id": "hello", "path": "…" })` — same-id, strictly
  newer version replacement with the usual rollback snapshot.
- `plugin_list()` — the registry view (id, version, enabled state,
  permissions, contributed capabilities).

Install and update copy through the same registry code as the settings
page and then reassemble the runtime, so the plugin's skills, tools,
MCP servers and hooks are live from the next turn on (`RebuildRuntime`
defers the swap while the calling turn still runs). Headless runtimes
wire an empty installer and expose none of these tools, and runs with
no interactive user to answer the confirmation (automations, headless)
fail closed instead of installing.

The `plugin-creator` builtin skill documents the manifest and bundle
contracts the agent needs to author a plugin from scratch.
