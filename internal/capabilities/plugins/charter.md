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
runs. The manifest carried a copy of the UI half once —
`contributes.settingsPanels`, `contributes.sidebarEntries`, `contributes.pets` — parsed and
rendered by nothing; the cleanup deleted the segments, and a manifest that
still writes one keeps loading.

`charter_test.go` enforces the tables in both directions: every permission,
manifest field, service and kraft primitive the code has must be claimed by a row,
and every row must name something that exists. `legacy` and `reserved` rows
carry the note that says what has to change for them to leave.

## Contributions — what a plugin gives

| kind            | consumer | declared by  | runs in    | grant            | anchor               |
|-----------------|----------|--------------|------------|------------------|----------------------|
| `ui.panel`      | ui       | registration | webview    | —                | `ctx.settingsPanels` |
| `ui.entry`      | ui       | registration | webview    | —                | `ctx.sidebarEntries` |
| `ui.command`    | ui       | registration | webview    | —                | `ctx.commands`       |
| `ui.status`     | ui       | registration | webview    | —                | `ctx.statusBar`      |
| `ui.pet`        | ui       | registration | webview    | —                | `ctx.pets`           |
| `agent.skill`   | agent    | manifest     | data       | `skills:provide` | `skills`             |
| `agent.hook`    | agent    | manifest     | data       | `hooks:provide`  | `hooks`              |
| `agent.mcp`     | agent    | manifest     | subprocess | `mcp:provide`    | `mcpServers`         |
| `agent.tool`    | agent    | manifest     | kraft      | `tools:provide`  | `tools`              |
| `platform.node` | platform | manifest     | kraft      | —                | —                    |

- **`ui.panel`** — a settings panel
  - comes alive when: the plugin bundle loads and apply() calls ctx.settingsPanels.add
  - removed by: the registration's disposer, which runs when the plugin scope ends: disable, update, unload or app teardown
- **`ui.entry`** — a sidebar entry
  - comes alive when: the plugin bundle loads and apply() calls ctx.sidebarEntries.add
  - removed by: the registration's disposer (disable, update, unload, app teardown)
- **`ui.command`** — a command in the command palette
  - comes alive when: the plugin bundle loads and apply() calls ctx.commands.add
  - removed by: the registration's disposer (disable, update, unload, app teardown)
  - note: commands:register was retired by the vocabulary sweep: every plugin may register commands, and the name has gated nothing since the Cordis port. A manifest that still declares it is accepted and the name ignored.
- **`ui.status`** — a status-bar widget
  - comes alive when: the plugin bundle loads and apply() calls ctx.statusBar.add
  - removed by: the registration's disposer (disable, update, unload, app teardown)
  - note: statusbar:contribute was retired by the vocabulary sweep, for the same reason as commands:register.
- **`ui.pet`** — a declarative pet pack (Rive specs) for the pet window
  - comes alive when: the bundle loads and calls ctx.pets.add; the Go registry validates the pack before any pet window mounts it
  - removed by: the disposer unregisters the pack and restores the builtin it overrode
  - note: pets:contribute was retired with the manifest copy of the segment it guarded; packs arrive through the registrar, and that path checks no permission.
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
| `secret.*`          | kraft   | `secret.get`, `secret.set`, `secret.delete`  | `secrets:auth`     | auth/<plugin>/... and inference/<plugin>/... only; the namespace prefix narrows what the grant already allows            |
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
  - note: the grant is checked when the primitive runs, the same check the webview ctx.secrets surface passes; P2 wired it into handleSecret, so kraft.go's package comment no longer claims a gate that does not exist.
- **`open.url`** — opens a URL in the OS browser
- **`inference.*`** — registers or removes a provider profile
- **`session.import`** — imports a transcript bundle into a workspace
- **`workspace.current`** — the active workspace path
- **`telemetry.*`** — points the app's OTLP export at the plugin's collector
- **`emit.event`** — forwards a plugin event to the host bus
  - status: reserved
  - note: accepted and discarded: the host event bus has no plugin-facing wiring. The row exists so the dispatch's case is claimed; wire it or delete it.

## Grants

Every permission in `plugins.AllowedPermissions` is spent by a row above.
Contribution grants are spelled `kind:provide` — a plugin provides a
contribution. `CheckPermissions` is fail-closed, so a name leaves the set only
by retirement (`RetiredPermissions`): the parser drops it on the way in and
logs once, which keeps every already-installed manifest loading.

- `skills:provide` — spent by `agent.skill`
- `hooks:provide` — spent by `agent.hook`
- `mcp:provide` — spent by `agent.mcp`
- `tools:provide` — spent by `agent.tool`
- `storage:kv` — spent by `storage`
- `secrets:auth` — spent by `secrets`, `secret.*`
- `sessions:import` — spent by `session.import`
- `telemetry:export` — spent by `telemetry.*`

Retired and ignored — a manifest that still declares one loads, the name is
dropped and logged once:

- `events:subscribe` — the Cordis event bus is always available (ctx.on); no gate was ever wired to the name.
- `commands:register` — the commands registrar is provided to every plugin; the name has been checked by nothing since the Cordis port.
- `statusbar:contribute` — the status-bar registrar is provided to every plugin, for the same reason as commands:register.
- `pets:contribute` — it gated only the manifest copy of pet packs, deleted once packs became registrations; packs arrive through ctx.pets, and that path checks no permission.

## Legacy manifest inputs

Older spellings a manifest may still carry, and what the host does with each:
`translate` loads under the new name, `ignore` accepts the manifest and drops
the name, `reject` refuses two spellings where one belongs.

| input                                  | action    | becomes          | why                                                                                                                                                                                                                           |
|----------------------------------------|-----------|------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `capability`                           | translate | `kraft`          | the subprocess section was renamed; the host reads the old key so installed plugins keep loading without an edit.                                                                                                             |
| `capability + kraft`                   | reject    | —                | two spellings of one manifest section is ambiguity, not compatibility: the manifest is refused and both names are named in the error.                                                                                         |
| `skills:contribute`                    | translate | `skills:provide` | the vocabulary sweep spells contribution grants kind:provide; the old spelling is translated silently.                                                                                                                        |
| `hooks:register`                       | translate | `hooks:provide`  | the vocabulary sweep spells contribution grants kind:provide; the old spelling is translated silently.                                                                                                                        |
| `mcp:contribute`                       | translate | `mcp:provide`    | the vocabulary sweep spells contribution grants kind:provide; the old spelling is translated silently.                                                                                                                        |
| `tools:expose`                         | translate | `tools:provide`  | the vocabulary sweep spells contribution grants kind:provide; the old spelling is translated silently.                                                                                                                        |
| `old + new spelling of one permission` | reject    | —                | declaring both spellings of one grant is ambiguity, not compatibility: the manifest is refused and both names are named in the error.                                                                                         |
| `events:subscribe`                     | ignore    | —                | the Cordis event bus is always available (ctx.on); the name gates nothing, so the manifest is accepted and the name is dropped and logged once.                                                                               |
| `commands:register`                    | ignore    | —                | the commands registrar is provided to every plugin, so the manifest is accepted and the name is dropped and logged once.                                                                                                      |
| `statusbar:contribute`                 | ignore    | —                | the status-bar registrar is provided to every plugin, so the manifest is accepted and the name is dropped and logged once.                                                                                                    |
| `pets:contribute`                      | ignore    | —                | it gated only the contributes.pets manifest copy, which nothing rendered and the cleanup deleted; packs register from the bundle, so the name is dropped and logged once.                                                     |
| `contributes`                          | ignore    | —                | the manifest copy of the UI half. Panels, sidebar entries and pet packs register from the bundle (ctx.settingsPanels.add / ctx.sidebarEntries.add / ctx.pets.add), so the segment is dropped; a non-empty one is logged once. |

## Manifest fields

Every JSON path the `Manifest` struct carries. A **contribution** path is an
anchor of the row named; a **skeleton** path is identity or runtime plumbing and
is listed here with what consumes it.

| path             | kind         | owner                                                                                                                                                                          |
|------------------|--------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `entry`          | skeleton     | the ES module the shell loads; the file is the entry, the contributions are registrations                                                                                      |
| `hooks`          | contribution | `agent.hook`                                                                                                                                                                   |
| `id`             | skeleton     | identity, and the namespace of everything the plugin owns: KV entries, secrets, pet packs                                                                                      |
| `kraft`          | skeleton     | the subprocess runtime the machine-half contributions run in: binary, protocol, and the open.url allowlist                                                                     |
| `mcpServers`     | contribution | `agent.mcp`                                                                                                                                                                    |
| `minHostVersion` | skeleton     | the release gate the host checks before loading                                                                                                                                |
| `name`           | skeleton     | display name                                                                                                                                                                   |
| `permissions`    | skeleton     | the grants the plugin asks for; the closed set is AllowedPermissions plus the retired names canonicalPermissions drops, with older spellings translated (LegacyManifestInputs) |
| `skills`         | contribution | `agent.skill`                                                                                                                                                                  |
| `tools`          | contribution | `agent.tool`                                                                                                                                                                   |
| `update`         | skeleton     | where the host looks for a newer version                                                                                                                                       |
| `version`        | skeleton     | release identity, and what an update compares against                                                                                                                          |

