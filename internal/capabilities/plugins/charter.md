# The plugin charter

<!-- Generated from charter.go by TestCharterDocument. Do not edit by hand:
     go test ./internal/capabilities/plugins -run TestCharterDocument -update -->

A plugin has two halves: the bundle the shell loads into a webview, and the
machine half the host runs for it. Everything a plugin can **give** the host or
**call** on it answers the same five questions, so the next contribution kind is
a row in one of the two tables below rather than a new mechanism:

| question | column |
| --- | --- |
| Who consumes it? | consumer |
| Where does its code run? | runtime |
| How does the host learn it exists? | declared by |
| What authorizes it? | grant |
| When does it live, and what removes it? | activation / teardown |

The declaration line is drawn per consumer, not per taste. The agent runtime
(and, later, the app platform) must enumerate a plugin's half *without executing
it*, so their contributions are declared in `plugin.json`; a UI contribution *is
code*, so its only source is the registration the bundle performs while it
runs. A **shadow** is a manifest segment that is still parsed, validated and
reported while nothing consumes it — the drift the registration rule exists to
prevent, kept visible until it is deleted.

`charter_test.go` enforces the tables in both directions: every permission,
manifest field, service and kraft primitive the code has must be claimed by a row,
and every row must name something that exists. `legacy` and `reserved` rows
carry the note that says what has to change for them to leave.

## Contributions — what a plugin gives

| kind            | consumer | declared by  | runs in    | grant               | anchor               |
|-----------------|----------|--------------|------------|---------------------|----------------------|
| `ui.panel`      | ui       | registration | webview    | —                   | `ctx.settingsPanels` |
| `ui.entry`      | ui       | registration | webview    | —                   | `ctx.sidebarEntries` |
| `ui.command`    | ui       | registration | webview    | —                   | `ctx.commands`       |
| `ui.status`     | ui       | registration | webview    | —                   | `ctx.statusBar`      |
| `ui.pet`        | ui       | registration | webview    | —                   | `ctx.pets`           |
| `agent.skill`   | agent    | manifest     | data       | `skills:contribute` | `skills`             |
| `agent.hook`    | agent    | manifest     | data       | `hooks:register`    | `hooks`              |
| `agent.mcp`     | agent    | manifest     | subprocess | `mcp:contribute`    | `mcpServers`         |
| `agent.tool`    | agent    | manifest     | kraft      | `tools:expose`      | `tools`              |
| `platform.node` | platform | manifest     | kraft      | —                   | —                    |

- **`ui.panel`** — a settings panel
  - comes alive when: the plugin bundle loads and apply() calls ctx.settingsPanels.add
  - removed by: the registration's disposer, which runs when the plugin scope ends: disable, update, unload or app teardown
  - shadow: `contributes.settingsPanels` — parsed, validated and reported in the plugin summary, but no surface renders it — the UI draws registered panels only. P3 of the framework plan deletes the segment.
- **`ui.entry`** — a sidebar entry
  - comes alive when: the plugin bundle loads and apply() calls ctx.sidebarEntries.add
  - removed by: the registration's disposer (disable, update, unload, app teardown)
  - shadow: `contributes.sidebarEntries` — the same shape as contributes.settingsPanels: validated, reported, rendered by nothing. P3 deletes it.
- **`ui.command`** — a command in the command palette
  - comes alive when: the plugin bundle loads and apply() calls ctx.commands.add
  - removed by: the registration's disposer (disable, update, unload, app teardown)
  - note: commands:register is in the sunset list: every plugin may register commands, and the permission has been checked by nothing since the Cordis port (plugins/hello still declares it).
- **`ui.status`** — a status-bar widget
  - comes alive when: the plugin bundle loads and apply() calls ctx.statusBar.add
  - removed by: the registration's disposer (disable, update, unload, app teardown)
  - note: statusbar:contribute is in the sunset list, for the same reason as commands:register.
- **`ui.pet`** — a declarative pet pack (Rive specs) for the pet window
  - comes alive when: the bundle loads and calls ctx.pets.add; the Go registry validates the pack before any pet window mounts it
  - removed by: the disposer unregisters the pack and restores the builtin it overrode
  - shadow: `contributes.pets` — validated (and gated by pets:contribute in its manifest gate) but read by no one — packs arrive through the registrar. P3 deletes the segment.
  - note: the live path is not permission-gated; pets:contribute guards only the shadow segment.
- **`agent.skill`** — a directory of agent skills, mounted as a skill root
  - comes alive when: the next runtime assembly after the plugin is enabled; assembly reads enabled plugins' manifests
  - removed by: the next assembly after disable or update drops the root; a turn already running keeps the skills it assembled
- **`agent.hook`** — a hooks.json the runtime adds as an extra source
  - comes alive when: the next runtime assembly after the plugin is enabled
  - removed by: the next assembly after disable or update drops the hooks; hooks the runtime already registered keep running for the turn in flight
- **`agent.mcp`** — an MCP server the agent may call
  - comes alive when: the next runtime assembly, which hands the servers to the MCP source; relative stdio commands resolve against the plugin directory
  - removed by: the next assembly after disable or update re-reads the manifest; a rebuilt runtime closes the connection
- **`agent.tool`** — an agent-callable tool backed by a kraft method
  - comes alive when: the next runtime assembly lists the tool; the kraft process itself starts lazily on the first call
  - removed by: the next assembly drops the spec; the process is stopped when the plugin is disabled, updated or unloaded
- **`platform.node`** — a compute node the app platform may schedule (not implemented)
  - comes alive when: not implemented
  - removed by: not implemented
  - status: reserved
  - note: not implemented: no manifest field, no consumer, no grant. The row is here on purpose, so the node kind arrives as a row plus an adapter instead of a seventh special case.

## Host interfaces — what a plugin may call

| interface           | surface | anchor                                       | grant              | scope                                                                                                                    |
|---------------------|---------|----------------------------------------------|--------------------|--------------------------------------------------------------------------------------------------------------------------|
| `react`             | webview | `ctx.react`                                  | —                  | the same module the app renders with, so plugin components share hooks and the reconciler                                |
| `ui`                | webview | `ctx.ui`                                     | —                  | the shell's own status line, and a picker the user drives                                                                |
| `host`              | webview | `ctx.host`                                   | —                  | the running app's version string                                                                                         |
| `i18n`              | webview | `ctx.i18n`                                   | —                  | the language tag only; plugins ship their own dictionaries and render through it                                         |
| `invoke`            | webview | `ctx.invoke`                                 | —                  | the plugin's own subprocess; the host routes by method name and interprets nothing                                       |
| `storage`           | webview | `ctx.storage`                                | `storage:kv`       | one namespace per plugin, for non-secret data                                                                            |
| `secrets`           | webview | `ctx.secrets`                                | `secrets:auth`     | auth/<plugin>/... only; a value never crosses into the webview                                                           |
| `secret.*`          | kraft   | `secret.get`, `secret.set`, `secret.delete`  | —                  | auth/<plugin>/... and inference/<plugin>/... only — the namespace prefix is the gate                                     |
| `open.url`          | kraft   | `open.url`                                   | —                  | hosts listed in kraft.hosts only — the manifest carries the allowlist                                                    |
| `inference.*`       | kraft   | `inference.upsert`, `inference.remove`       | —                  | rows whose credential lives in the plugin's own secret namespace; the user-owned enabled flag is not the plugin's to set |
| `session.import`    | kraft   | `session.import`, `session.imported_sources` | `sessions:import`  | writes a conversation the host then owns; repeated bundles dedupe by source                                              |
| `workspace.current` | kraft   | `workspace.current`                          | —                  | the path only, read on every call                                                                                        |
| `telemetry.*`       | kraft   | `telemetry.configure`, `telemetry.disable`   | `telemetry:export` | one plugin sink at a time, dropped when the plugin is disabled or dies; header values stay in host memory                |
| `emit.event`        | kraft   | `emit.event`                                 | —                  | nothing yet                                                                                                              |

- **`react`** — the host's React instance
- **`ui`** — transient status flashes and the native folder picker
- **`host`** — read-only host metadata
- **`i18n`** — the host's current language
- **`invoke`** — calls a method on the calling plugin's own kraft
- **`storage`** — the plugin's own key/value store
- **`secrets`** — existence and delete for secrets in the auth scope
- **`secret.*`** — read, write and delete the plugin's own secrets
  - note: kraft.go's package comment says these primitives are gated by secrets:auth; no code checks that permission — the namespace prefix is what stops a plugin from touching another's secrets. P2 decides: enforce the grant or drop the claim.
- **`open.url`** — opens a URL in the OS browser
- **`inference.*`** — registers or removes a provider profile
- **`session.import`** — imports a transcript bundle into a workspace
- **`workspace.current`** — the active workspace path
- **`telemetry.*`** — points the app's OTLP export at the plugin's collector
- **`emit.event`** — forwards a plugin event to the host bus
  - status: reserved
  - note: accepted and discarded: the host event bus has no plugin-facing wiring. The row exists so the dispatch's case is claimed; wire it or delete it.

## Grants

Every permission in `plugins.AllowedPermissions` is either spent by a row above,
or listed here as accepted-and-unspent. `CheckPermissions` is fail-closed, so a
name leaves the set only together with the manifests that declare it.

- `skills:contribute` — spent by `agent.skill`
- `hooks:register` — spent by `agent.hook`
- `mcp:contribute` — spent by `agent.mcp`
- `tools:expose` — spent by `agent.tool`
- `storage:kv` — spent by `storage`
- `secrets:auth` — spent by `secrets`
- `sessions:import` — spent by `session.import`
- `telemetry:export` — spent by `telemetry.*`

Accepted, spent by nothing:

- `events:subscribe` (legacy) — the Cordis event bus is always available (ctx.on); no gate was ever wired to the name. Accepted so installed manifests keep validating; P2 deletes it from the permission set.
- `commands:register` (legacy) — the commands registrar is provided to every plugin; plugins/hello still declares the permission. P2 deletes it.
- `statusbar:contribute` (legacy) — the status-bar registrar is provided to every plugin; plugins/hello still declares the permission. P2 deletes it.
- `pets:contribute` (legacy) — read by manifest validation, and only to gate the inert contributes.pets segment; the live registrar path checks no permission. P3 deletes the segment, and the grant goes with it.

## Manifest fields

Every JSON path the `Manifest` struct carries. A **contribution** path is an
anchor of the row named; a **skeleton** path is identity or runtime plumbing and
is listed here with what consumes it.

| path                         | kind         | owner                                                                                                      |
|------------------------------|--------------|------------------------------------------------------------------------------------------------------------|
| `contributes.pets`           | shadow       | `ui.pet`                                                                                                   |
| `contributes.settingsPanels` | shadow       | `ui.panel`                                                                                                 |
| `contributes.sidebarEntries` | shadow       | `ui.entry`                                                                                                 |
| `entry`                      | skeleton     | the ES module the shell loads; the file is the entry, the contributions are registrations                  |
| `hooks`                      | contribution | `agent.hook`                                                                                               |
| `id`                         | skeleton     | identity, and the namespace of everything the plugin owns: KV entries, secrets, pet packs                  |
| `kraft`                      | skeleton     | the subprocess runtime the machine-half contributions run in: binary, protocol, and the open.url allowlist |
| `mcpServers`                 | contribution | `agent.mcp`                                                                                                |
| `minHostVersion`             | skeleton     | the release gate the host checks before loading                                                            |
| `name`                       | skeleton     | display name                                                                                               |
| `permissions`                | skeleton     | the grants the plugin asks for; AllowedPermissions, the two tables and SunsetGrants are the closed set     |
| `skills`                     | contribution | `agent.skill`                                                                                              |
| `tools`                      | contribution | `agent.tool`                                                                                               |
| `update`                     | skeleton     | where the host looks for a newer version                                                                   |
| `version`                    | skeleton     | release identity, and what an update compares against                                                      |

