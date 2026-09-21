# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Mid-turn steering: a message submitted while a turn is running now
  reaches the model at the next tool-round boundary instead of waiting
  for the turn to end. Enter in the composer sends it
  (`Conversation.Steer`); the model reads it as a standalone user
  message right after the round's tool results, and several corrections
  waiting at the same boundary arrive as one message. A boundary
  injects at most one core-sized steer (32 KiB of text); a submit that
  would exceed that is refused with the text left in the caller's
  hands. Whatever a turn cannot deliver comes back as a card with
  resend/dismiss instead of disappearing with the conversation archive,
  and a turn result whose undelivered count cannot be read keeps every
  such row rather than assuming delivery. (#181)
- Unattended automations get a per-task run limit: `timeout` bounds one
  run (whole minutes, empty = the 15-minute default, at most 24h). A run
  that outlives it is cancelled, its record says `timeout` with the
  bound, and the concurrency slot comes back once the turn settles. The
  editor gains a run-limit field and the run list a timeout badge.
  (#181)
- A live scheduled run can be stopped from the automations panel:
  `Automation.CancelRun` cancels the run context the manager owns, the
  desktop runner's bounded wait turns that into the host's turn cancel,
  and the record is written as `canceled` with no error text once the
  turn settles (archive, memory commit, usage and the concurrency slot
  all still run). The run list offers the action on a running row and
  the task menu on a task whose run is live; the failure notification
  policy stays quiet for a stop the user asked for. (#186)

### Changed

- Enter in the composer now steers the running turn instead of
  interrupting it; Tab keeps the after-turn queue, a draft carrying
  attachments is refused with a hint rather than changed silently, and
  barge-in moves to Cmd/Ctrl+Enter and to a stop button that stays
  reachable while a draft is being written. (#181)
- Automations saved without a run limit are bounded by the 15-minute
  default. Tasks stored before the limit existed are not: the migration
  seeds them with the one-hour bound they used to run under (the
  assistant graph's run timeout), so an upgrade shortens no run nobody
  asked to shorten — the 15-minute default is what a task saved without
  a limit gets, not what existing tasks are given. (#181)
- Users overriding the assistant graph from disk (a `{file: …}`
  `assistant.yaml`) need the graph's new `steer` node too: copy
  `graphs/nodes/steer.js` alongside it, or the graph references an
  asset the deployment does not have and turns stop starting. Graphs
  left at the shipped defaults pick it up on their own. (#181)

### Fixed

- A correction typed while a scheduled automation runs in the open
  workspace is reported like one typed into an interactive turn: the
  automation's terminal event carries the undelivered-steer count, so
  the text becomes a resend/dismiss card instead of vanishing from the
  transcript on the next archive reconciliation. (#181)
- A wait cancelled before its turn settled no longer crashes the turn
  goroutine: the settle path tolerates a result-less wait, so the turn
  still archives and the caller still sees the cancellation. (#181)
- The `automation` tool's nested `task`/`schedule` schema now reaches the
  model intact. Nested properties were built as `ToolPropertyDef`
  values, whose schema fields are unexported, so each one marshalled as
  an empty object: the model saw property names with no type and no
  description — including the run-limit field — and no allowed values
  for the schedule kind. Nested properties are raw JSON Schema maps
  now, like every other tool in the repo.
- A staged draft (the Tab queue, or Enter pressed while the previous
  send was still starting) runs in the workspace that owns its
  conversation instead of the one on screen. The start used to resolve
  its workspace from the active window, so a draft queued behind a
  turn in another workspace was attached there as a brand-new session —
  with that workspace's working directory and sandbox mode — and the
  message never reached the conversation it was meant for. The start
  request now carries the owning workspace, a workspace the window has
  left is served by its own background Host (the UI's current Host is
  never taken over), and a start for a conversation no store owns is
  refused rather than minted somewhere.

## [0.5.3] - 2026-09-17

### Added

- `web_search` gives a deployment a search path that does not depend on
  the provider hosting one: auto mode spreads conversations across the
  keyless Parallel and Exa hosted MCP endpoints and fails over to the
  other once, while Tavily and Brave call with a key the user supplies
  (stored as `websearch/<provider>`; the document keeps only the
  secret reference). The tool returns links plus a bounded excerpt
  block — `web_fetch` still owns reading pages in full — and Exa's
  free-tier notice, which arrives as an ordinary 200 text payload, is
  mapped back to a rate-limit error so it cannot masquerade as "no
  results". Responses are capped at 1 MiB with a 15s default timeout
  and at most 10 results, the model may only set query, count,
  freshness and domains, and the host always owns the endpoint.
  Settings > Tools gains the matching card: provider, key, result
  count, timeout and endpoint override under advanced, and a real test
  query. An override must be https (plain http only on loopback) and
  rejects query, fragment, userinfo and redirects. (#157)
- Settings > Tools is one page for the generation tools and the MCP
  servers: an item opens the same centered dialog MCP details use,
  listing every enabled deployment that serves that output with the
  provider-specific knobs its driver declares — the app's own enum,
  number and unset/on/off controls, each next to the default the
  driver documents and a one-line explanation. Presets stage the
  common requests (no watermark, 2K, transparent background, with
  audio, fixed camera) and nothing is stored until the user saves,
  because an unset knob is the state that keeps the provider in charge
  of its own default. Values persist in the user layer under
  `resources.tool.imagegen` / `resources.tool.videogen` keyed by
  deployment id, and the vocabulary, validation and pruning live in
  `foundation/config`: an unknown provider, an out-of-range or unknown
  enum value and a knob block whose provider was removed all fail
  before anything is written. (#152, #154, #155, #156)
- A command the OS sandbox refuses is no longer a dead end:
  `exec_command` recognises a confinement refusal in the finished
  command's output (EPERM/EROFS/access-denied phrasings, including
  PowerShell's .NET wording; a plain "permission denied" deliberately
  does not match), asks whether it may run outside the sandbox once,
  and re-runs the exact same argv with the full environment, marking
  the result with a `note`. "Always" remembers the rule in the
  workspace's `escalations.yaml` — deliberately its own file, because
  a downgraded build rewrites `approvals.yaml` from a narrower struct
  — and every decision (once / always / deny) is appended to
  `<workspace>/audit/escalations.jsonl`. Only workspace sessions
  escalate (read-only keeps its guarantee, YOLO has no confine), only
  `exec_command` is prompted, remembered rules cover one-shot commands
  but not TTY sessions, and a host with no user to ask keeps the
  original failure. Settings > Permissions lists and revokes the
  rules. (#146)
- The process PATH is resolved once at startup and used by every
  spawn: `foundation/utils/envpath` appends the user's prepend list,
  the inherited PATH and the platform's install directories (Homebrew
  on macOS; `/usr/local/bin`, snap and Linuxbrew on Linux; plus the
  per-user bin directories), removes nothing but empty entries and is
  idempotent, so a Dock-launched app can find Homebrew, npm-global and
  user-local binaries for MCP stdio servers, agent commands, `gh` and
  `git` alike. Settings > Diagnostics gains a Process PATH card that
  groups the entries by origin, flags rejected and missing
  directories and edits `desktop.json`'s `path.prepend`; saving
  re-resolves and reloads the runtime so MCP servers reconnect with
  the new environment. (#147, #148)
- Generated images and videos play inside the app. The desktop serves
  workspace media over a loopback endpoint (127.0.0.1 on a random
  port, a per-launch random token, GET/HEAD only, traversal and
  symlink escapes rejected, byte-range seeking), the viewer plays
  videos inline and falls back to the system player, and generation
  cards render thumbnails, inline players and a streamed-preview
  section. The image tool gained count, seed, quality, reference
  images, mask and partial-image previews, and the video tool gained
  aspect ratio, seed, bookend frames and reference videos/audios, with
  the input roles validated instead of left to the provider. (#151)

### Changed

- flowcraft core moves to v0.4.4 and the drivers to v0.3.2, carrying
  the upstream fixes this repository reported: generate fallback skips
  a target whose declared outputs cannot serve the request, and
  `tool_search` reports per-round visibility instead of pool
  membership, so a name that loses the round's budget answers
  `visible_budget`, an oversized definition no longer stops the byte
  walk, and the best-ranked hit of a batch wins the tie. Provider
  knobs left the model-facing schemas with this work: `generate_video`
  no longer offers thirteen Seedance/MiniMax knobs, `generate_image`
  keeps only its per-call inputs, and both decode arguments strictly,
  so a knob the schema does not offer fails loudly. The per-round tool
  budget rises to 64 definitions / 48 KiB with a 48-tool / 32 KiB
  discovery pool, measured against the embedded catalog, and tool
  results list the fields a driver dropped instead of dropping them
  silently. (#152, #153, #156)
- Hosted web search is drift-proof: `StartRun` filters the board
  extension bag against the live runtime generation's decoders, so an
  entry naming a deployment the serving generation does not have is
  dropped with one warning instead of failing the turn at the
  inference node. Eligibility resolves the driver impl rather than the
  catalog provider id, which restores the checkbox for custom rows
  and rows with an explicit driver, `deepseek/deepseek-v4-pro` is
  marked search-capable, and the checkbox explains which upstreams
  honour the tool. (#149)
- Windows gets a real shell: `foundation/utils/shelldetect` resolves
  PowerShell 7 → Windows PowerShell 5.1 → `cmd.exe`, skipping the
  Store execution-alias stubs a restricted token cannot follow, and
  the exec tool, the description the model reads, execd's environment
  report and the diagnostics page all read it instead of the
  hard-coded `/bin/sh -c` that made shell-syntax commands fail there.
  Windows bare words accept `\`, so `C:\tools\x.exe a b` stays on the
  direct-argv path. (#146)

### Fixed

- Settings > Diagnostics no longer blanks the page on a healthy
  machine: the PATH DTO serialized its empty list fields as `null` and
  the card read them as arrays, which fired on any machine without a
  rejected or missing directory. The lists are normalized on the wire
  and read defensively, the candidate table is split per platform so
  macOS no longer reports `/snap/bin` as "not installed", and a
  prepend directory that does not exist yet is flagged in place
  instead of disappearing. (#148)
- The pet is pulled back onto a display after a monitor is unplugged
  or the layout changes: the placement check clamps a stranded
  position onto the closest work area rather than the primary
  display's corner, keeps adopting an OS-reported position that is
  still on a display, skips a drag in progress, and measures the drawn
  character rather than the transparent stage around it. (#150)
- Generation-only models no longer sit in the composer and automation
  model pickers, where a chat turn could never route to them: the
  option list keeps only models that can serve a text-output chat
  request, which also lets the Auto reasoning hint resolve against a
  real text target. (#151)
- A stdio MCP server whose command cannot be spawned is pre-flighted
  (PATH for bare names, the child PATH for `#!/usr/bin/env`
  interpreters) and reported with the concrete reason in both the
  status list and the test button, instead of retrying forever behind
  a "connecting" status. (#146)

## [0.5.2] - 2026-09-16

### Fixed

- `execd` treats a client that already hung up as teardown instead of
  warning about the failed write, so a session watcher firing after a
  client disconnects no longer leaves a spurious record in the app log
  (or, in tests, in the next test's capture). (#143)

## [0.5.1] - 2026-09-16

### Added

- The agent can author and install plugins without leaving the
  conversation: `plugin_install`, `plugin_update` and `plugin_list`
  copy a plugin from a workspace directory or `.zip` package into the
  same registry Settings > Plugins uses and reload the runtime
  afterwards, so a plugin written in the workspace is live from the
  next turn on. Every mutation asks the user to confirm first, and the
  dialog shows the id, version, permissions, entry bundle and
  capability binary before anything is copied. Sources keep the file
  tools' workspace confinement (absolute paths, traversal and symlink
  escapes are rejected outside YOLO sessions), while manifest
  validation, version ordering, builtin shadowing and zip-slip checks
  stay with the registry. A runtime with no interactive user —
  automation runs, headless — fails closed and exposes none of the
  three tools, and the new built-in `plugin-creator` skill documents
  the manifest, bundle and capability-subprocess contracts. (#139)

### Changed

- `google.golang.org/grpc` moves to v1.83.1 with the OpenTelemetry
  modules at 1.44.0, clearing GO-2026-6348 — heap exhaustion through
  HTTP/2 DATA frame fragmentation — which the vulnerability database
  published against the 0.5.0 pin after that release. (#137)

### Fixed

- A stopped `execd` child no longer loses the stderr lines it writes
  while shutting down: the forwarder ran detached and `exec.Cmd.Wait`
  closed the pipe's parent end the moment the child was reaped, so
  shutdown lines could be dropped, or land after stop returned — in
  the next session's log window and, in CI, the next test's capture.
  The pipe is explicit now, the forwarder drains to EOF, and stop
  waits for it under the same bounded teardown grace period — which is
  only reachable while a grandchild holds the write end, because the
  parent releases its own copy when the child starts. Without that
  release the read end never saw EOF, so stop always fell through to
  the grace period, dropped the child's last lines, leaked a
  descriptor per child and could still emit a line after returning.
  (#138, #141)

## [0.5.0] - 2026-09-16

### Added

- Settings > Interface gains appearance settings: the interface and
  code fonts are chosen from the families the host enumerates
  (CoreText on macOS, GDI on Windows, fontconfig on Linux) with a
  searchable picker, and a text-size scale applies through CSS
  variables. The choice lives in the desktop preference document with
  a localStorage mirror, so the first frame paints before the binding
  resolves. (#130)
- Built-in model template catalog: `foundation/config` embeds
  `inference_templates.json` (read as `Config.InferenceCatalog`)
  whose entries reuse `InstanceSpec`/`ModelSpec`, decode strictly,
  record their source and are validated through the same path a
  settings save takes. Settings shows templates as one-click pills
  next to the driver picker plus a searchable model list per row;
  picking a model only writes model fields, and a declaration that
  needs a provider-level fact (video input) warns instead of editing
  the provider behind the user's back. (#127)
- Capability plugins that declare `telemetry:export` can point the
  app's OTLP export (logs, traces, metrics) at their own collector
  through `telemetry.configure` / `telemetry.disable`. Collector
  credentials stay in memory, one sink is active at a time,
  `OTEL_EXPORTER_OTLP_ENDPOINT` keeps priority, and the sink is
  dropped when the plugin is disabled, uninstalled or crashes. The
  switch sits in Settings > Diagnostics next to the log viewer, and
  install/remove/deny changes land in
  `~/.opencraft/audit/telemetry.jsonl`. (#134)
- `view_image` returns workspace images to the model, oversized prompt
  images are downscaled, and models that declare video input get the
  bytes passed through, all on flowcraft's multimodal tool results.
  (#119)
- The pet reacts to hover — a wave on the first hover of a session, a
  glance afterwards — and walks while it is dragged, turning to face
  the direction of travel. (#131, #132)

### Changed

- flowcraft core upgraded to v0.4.1 with drivers v0.3.0: the
  per-vendor drivers collapse into one per wire family, tool results
  carry multimodal content, and the inference layer no longer ships a
  built-in model catalog or vendor presets — drivers are declared as
  deployment data and the user document is rendered from a pinned
  template. (#119)
- Turns are bounded by a run timeout instead of a node budget:
  `build.max_iterations: 0` lifts the graph loop guard and the agent
  policy's `run_timeout` (1h) ends the run, and a turn that ends on a
  deadline carries `error_kind: timeout` and renders a localized
  notice. (#133)
- Inference configuration has one row contract: `InstanceSpec` /
  `ModelSpec` are the single wire shape shared by the settings page
  and `inference.upsert`, `Lower` is the only way in, and the write
  rules live in `foundation/config`. The opaque `provider_spec` bag is
  gone (every provider-spec leaf the drivers accept is typed,
  including `reasoning_scope`), a plugin-declared vendor row no longer
  blocks settings saves, a managed row's `enabled` toggle belongs to
  the user and survives a plugin re-upsert, and plugin profiles are
  decoded strictly — `id` → `stable_id`, `provider_spec` →
  `advanced`, `enabled` no longer plugin-settable, with no
  compatibility shim. (#123)
- The inference advanced panel is grouped by driver section and uses
  the page's own dropdowns, and the model input-token field is wider
  with a hint that no longer claims a driver default that does not
  exist. (#124)
- Path containment is single-sourced: `foundation/utils/pathsafe`
  (`Within`/`Rel`/`ResolveUnder`/`RelRef`/`RealWithin`/`RealDir`) now
  backs the file/git bindings, skill installs, media attachments and
  plugin manifest/zip-slip checks that previously hand-wrote the
  comparison in fourteen places. (#120)
- Both JS graph mirrors are gated by fixtures generated from Go:
  `compact.js` against `summarytext` (including the summary prefix)
  and `world.js` against `worldstate`, with the real embedded scripts
  run by `frontend/src/lib/compactMirror.test.ts` and
  `worldNodeMirror.test.ts`. Writing the compaction gate fixed two
  real bugs: structured `tool_result` content counted as
  `[object Object]`, and tool-call arguments were estimated from a
  spelling Go does not render. (#120)
- The desktop pet no longer roams: it stands still while idle and only
  walks to its watch spot while the agent works or asks. The window
  anchors on the character's drawn pixels instead of the window
  rectangle, the stage shrank to 168px with hit-testing that only
  starts an interaction on visible pixels, and the disposition is
  renamed to idle (Settings > Diagnostics shows "Idle"). (#128, #131,
  #135)
- Automation notifications no longer ride the UI event bus:
  `Shell.Notify` hands a payload straight to the notification sink
  without requiring an attached window, notification kinds are named
  constants, and the banner copy is assembled by a unit-tested shaper.
  (#121)
- Child diagnostics reach the app log: the execd child installs a
  stderr-only sink forwarded line by line with `execd.pid` /
  `execd.socket` tags, capability-plugin stderr is forwarded as WARN
  records with `plugin.id` / `plugin.line`, normal teardown is no
  longer logged as a warning, and sockets older than a day are swept
  once per process. (#135)
- A plugin write that renders an unchanged inference document no
  longer rebuilds the runtime, and the assistant graph declares an
  explicit default branch for its two splits instead of relying on
  their routing variables being booleans. (#135)
- Formatting is checked with the toolchain `go.mod` pins instead of
  whatever `gofmt` is on PATH, imports are grouped by goimports
  through golangci-lint, and the last dead UI compatibility branches
  (the legacy turn marker strip and `artifact_sync`) were removed
  after confirming that no build ever persisted them. (#122)

### Fixed

- Conversations containing a tool call from before flowcraft core
  v0.4.0 can be resumed again: workspace migration 015 rewrites the
  string content of tool results in `archive_messages` and
  `memory_items` into canonical parts, and the desktop transcript
  reads those parts for archived and live results alike. (#125)
- `view_image` output is no longer dropped by the result budget: the
  raw cap dropped to 786000 bytes so base64 expansion plus the JSON
  envelope fits in 1 MiB, and oversized files are rejected through
  `workspace.LimitedReader` instead of being read whole. (#126)
- `apply_patch` accepts `input` as an alias for `patch` and rejects
  unknown keys by name, so models that emit the codex-harness shape no
  longer produce an empty patch; the tool card only asks the backend
  to render a patch when the arguments carry one, and its copy button
  is no longer nested inside the clickable row toggle. (#129, #135)
- The usage trend defaults to all models: an empty model aggregates
  the series across every model, and the picker offers an "All models"
  entry instead of preselecting the lifetime top model — a name that
  may no longer run, which left the chart empty while the table was
  full. A range without usage now says so. (#129)
- Turn errors are classified from types instead of the rendered prose:
  `host.ClassifyRunError` reads `interrupt_cause` from
  `agent.InterruptedError.Cause` and `error_kind` from
  `inference.Error.Kind` (plus a harness-level timeout), migration 014
  adds the columns and backfills existing rows with rules more
  tolerant than the renderer they replace, and the frontend only reads
  fields — the text parsing and its generic fallbacks are gone. (#121)
- Pet hover used to fire only when OpenCraft happened to be frontmost,
  because a webview sees mouse moves only while it is the key window:
  hover is now polled from the global pointer (NSEvent on macOS,
  GetCursorPos on Windows), the hit box is the drawn box plus a 4px
  pad, hover also fires while the pet is working or asking, and
  Diagnostics shows the polled pointer state. Linux has no
  global-pointer source yet, so hover stays inactive there. (#132)
- A capability plugin that writes a response larger than 64 KiB no
  longer hangs the host: the scanner got an explicit 8 MiB cap, a
  failed read logs the plugin id and stops the process, and a cached
  process whose done channel is already closed is not handed out.
  (#135)
- The pet reads the runtime's view model count instead of probing one
  index past the end, which logged a console error on every mount.
  (#135)

## [0.4.1] - 2026-09-11

### Added

- Desktop pet packs receive a `facing` value slot (`left` / `right`)
  so a character can render turn-around poses; the direction comes
  from the rover's own step delta, and standing still, sleeping,
  dragging or OS re-anchoring keep the last direction. (#116)
- The pet surface drives the pack's Rive view model instead of state
  machine inputs: one value per activity slot, intents fired only
  when the intent sequence moves, and a degraded marker plus a mount
  report in Settings > Diagnostics when a character cannot be
  driven. (#113)

### Changed

- The builtin assistant character moved to a view-model asset
  (artboard `Pet`, state machine `PetSM`, view model `PetVM`) with
  pack contract v2; the asset contract and the runtime accessors the
  renderer drives are pinned by tests. (#113)
- Sleeping is a persistent value instead of a re-fired `nap`
  one-shot, and the pet surface parks its renderer while the window
  is hidden or the character has been still. (#113)
- flowcraft drivers bumped for the Responses API replay fixes:
  assistant text rides an output message and replayed reasoning items
  carry their required summary, so multi-turn OpenAI, Azure and
  DeepSeek conversations no longer fail with HTTP 400 from the second
  turn on. (#114)
- The assistant graph's `build.max_iterations` rose from 400 to
  40000, leaving the graph loop guard as a cycle safety net rather
  than a budget long tool work can exhaust. (#112)

### Fixed

- A deploy layer that references a retired `OPEN_CRAFT_*` variable
  now fails with an error anchored to the file, the line and the
  `${ocraft:*}` replacement, and Settings > Diagnostics offers a
  repair that removes the offending declarations, keeps a `.bak`
  copy and reports the paths it removed. References that still
  resolve, such as a name exported in the process environment, keep
  working. (#117)

## [0.4.0] - 2026-09-10

### Added

- Desktop shell migrated to Wails v3 (pinned to `v3.0.0-beta.17`):
  `main.go` is the single v3 entry, `internal/adapters/desktop` holds
  the `core` composition root, 14 binding services and the root
  `Desktop`/`Shell`, and the shell gains multi-window, events, tray,
  single-instance, Dock reopen, async quit confirmation and native
  dialogs with the app icon. The v2 adapter, fyne systray and objc
  window hacks are gone. (#106)
- Roaming desktop pet: a transparent, frameless, always-on-top window
  that wanders across screens, perches on the main window, reacts to
  drags and clicks and exposes a pause/resume/disable menu, powered
  by an event-driven activity feed and a declarative Rive pack
  contract, plus a Subagent Dock in the main window. (#108)
- System notifications for interact prompts, finished turns and
  background automations are now raised from Go, so they still arrive
  when the webview is suspended (window hidden, minimized or parked
  in the tray). (#107)
- Diagnostics tab with persisted local metric series: backend turn
  and startup performance, renderer RUM and renderer errors, charted
  with a refresh control. (#106)

### Changed

- Subagent declarations upgraded to a versioned, Definition-shaped v1
  format with strict decoding, so hand-authored files cannot look
  like they control host-owned wiring; legacy flat records still load
  and are rewritten as v1, and unsupported versions fail loudly.
  (#109)
- flowcraft core upgraded to v0.3.1 and adapted to the reworked
  dynamic tool discovery model: a discovery pool with
  `max_tools`/`max_bytes`/`idle_rounds` and LRU eviction, and a
  `tool_search` that auto-loads matches and reports
  `hits`/`exposed`/`failed`/`evicted` instead of `selected`. (#110)
- The desktop pet is labelled experimental in Settings > General and
  Settings > Diagnostics. (#111)
- Desktop packaging and CI moved to the wails3 Taskfile: Linux jobs
  install GTK4 before generating bindings, and the macOS build cleans
  stale `.app` bundles before rebuilding. (#106)

### Fixed

- macOS packaging restored the `com.GizClaw.opencraft` bundle
  identifier and app-bundle naming, and the app icon is generated
  from a PNG instead of the icon-composer template. (#106)
- Windows installer shortcuts stamp the toast AppUserModelID, and
  frameless window options were restored for Windows and Linux.
  (#106)
- The automation host is wired into engine runtime assembly again.
  (#106)

## [0.3.2] - 2026-09-08

### Added

- Workspace Git rail in the right panel (a Files | Git switcher that
  appears when the workspace is a repository): staged, unstaged,
  untracked and unmerged change groups with inline diff previews,
  commit history with changed-file and diff drill-down, and a branch
  list with create/checkout. Commit, stage/unstage, discard, clean,
  pull and push (force behind a strong confirmation) are refused
  while a turn is active in the workspace. (#103)
- GitHub pull request review surface with the PR summary rendered as
  styled markdown, comments and changed files. (#103)
- Provider request and response ids are persisted on archived turns
  and surfaced for correlation. (#104)

### Changed

- Git reads and writes go through bounded services: snapshots and PR
  lookups are read-only, and writes are serialized and audited.
  (#103)
- flowcraft core upgraded to v0.3.0 with core-aligned drivers, and
  the assistant graph sets `stream_failure_policy.on_error: discard`
  so provider failures and truncated streams no longer commit a
  half-written assistant message. Interrupts keep `commit_partial`
  for archive/resume context. (#104)

### Fixed

- Turn start and delete retry transient lifecycle failures, retired
  hosts are marked stale and surface retryable errors, and the
  desktop rebuilds them after draining instead of reusing a stale
  runtime. (#103)

## [0.3.1] - 2026-09-08

### Added

- The current conversation can now be deleted: any live turn is
  cancelled, its terminal state is persisted, and only then are the
  session rows and files removed. When the deleted chat is still the
  active one a fresh session is minted in the same call. (#100)
- Quitting the app only asks for confirmation when an enabled
  scheduled task would stop running. (#100)

### Changed

- Saving memory, inference/router, MCP, or general config settings
  reloads the running runtime in place through an atomic generation
  swap instead of tearing it down. (#100)
- Runtime rebuilds are deferred to idle: when a config or plugin
  change lands during an active turn, the old runtime keeps serving
  until the turn finishes, so no second runtime ever serves the same
  conversation. (#100)
- Engine assembly resolves workspace paths through a deploy-time
  resolver instead of process environment variables, the user-level
  database lifecycle (usage/automations) is owned by host.Manager,
  and an import-boundary gate enforces internal layering. (#99)

## [0.3.0] - 2026-09-07

### Added

- Session history tree with per-turn grouping, plus a workspace picker
  for new chats; session history polish with persistent start titles
  for fresh conversations. (#82, #83)
- Hosted web search is now effective per deployment through board
  extensions seeded from the inference configuration. (#84)
- The host app version is exposed to plugins. (#85)
- Session import preserves per-turn timing and usage records. (#87)
- Configurable new-session defaults, with general and display settings
  split into separate pages. (#88)
- A YOLO-only build profile that pins every session to YOLO mode. (#89)
- Large image attachments: oversized PNG/TIFF/BMP images are normalized
  to upright JPEG q90 for preview and persistence (40MP decode cap,
  shared 10 MiB inline budget); non-image file attachments are no
  longer size capped. (#90)
- Queued/barge-in composer inputs: Enter while a turn runs interrupts
  it at its next safe point and sends the new message afterwards; Tab
  queues a draft that auto-sends when the current reply completes
  successfully. (#90)
- Barge-in wait UX: a banner above the composer shows the interrupt
  wait and exposes a force-cancel that hard-cancels the superseded
  run; cancelling a queued draft (X) restores it into the composer
  instead of discarding it. (#97)
- Clipboard image paste with per-type attachment badges. (#91)
- Workspace file viewer with inline code and PDF previews. (#92)
- Per-model usage dashboard with name-only accounting. (#93)

### Changed

- flowcraft upgraded to core v0.2.7: model declarations and inference
  settings adopt the new reasoning capability model (canonical effort
  maps per model), each driver's built-in model catalog is offered as
  a combobox with deprecated-model markers and catalog-derived
  defaults, and embedding model configuration is removed. (#95, #96)
- Conversation prompts are serialized as fragments and tool history is
  replayed structurally, so resumed turns rebuild the same prompt
  shape. (#94)
- The subagent right-sidebar panel was removed from the chat UI. (#86)

## [0.2.0] - 2026-09-04

### Added

- Automations: scheduled tasks stored in user.db with a recurrence
  engine, a background runtime pool, a task list UI, and an agent tool
  flow with preview-confirm and guided creation. (#58)
- Plugin agent capabilities: skills, MCP, hooks, and tools exposed to
  agents (namespaced with `__`), a plugin detail drawer with
  environment editing, and read-only plugin skills. (#56, #62)
- Plugin update/rollback with remote update checks. (#57)
- Session import, a settings Import tab, and `pickFolder` exposed to
  plugin UI services. (#66, #79, #80)
- Conversation fork: a turn can be forked into a fresh session
  together with its memory.
- Chat history artifacts, timestamps, and tool grouping, with earlier
  history auto-loading at the top of the transcript. (#67, #69)
- Skill drawer, workspace removal/close UX, and unconfigured-inference
  state. (#63)
- Headless `opencraft run` mode with JSONL output, plus the M1–M4 test
  infrastructure. (#61)
- Lazy, compressed language/toolchain runtimes without a bundled Go;
  background prep shows progress in the status bar. (#70, #72, #73)

### Changed

- The built-in `code-review` skill was removed; review behavior is
  left to installed user/repo skills and the agent's review guidance.
- flowcraft upgraded to core v0.2.3/v0.2.4 and drivers v0.2.1.
  Reasoning capabilities use the new `ReasoningCapability` object
  (kind + canonical-to-wire effort map) end to end, including
  `effort_none` for OpenAI/Azure; thinking effort gains the canonical
  `minimal` and `xhigh` levels in chat and automations. (#59, #66)
- The inference settings page offers each driver's built-in model
  catalog as a combobox: capabilities prefill from the built-in with
  one-click reset, deprecated models show ⚠️ plus their replacement,
  and provider default models come from the first non-deprecated
  catalog entry. (#59)
- Desktop exit polish: real quits ask for confirmation first, the
  tray/menu-bar icon uses the full-colour app icon and shows the app
  version, and the chat composer gained markdown editing and UI
  warnings. (#64)
- Artifact context menus and stream rendering polish. (#67)
- The desktop backend was consolidated onto a shared Host, session and
  conversation state moved to an XState layer, and turn/plugin state
  plus stream reconciliation were hardened. (#74, #75, #76)
- Tooling, secrets, and long-session handling hardened; ANSI escape
  sequences are stripped from tool results. (#55, #60)

### Fixed

- macOS amd64 terminate-flag conversion made portable across cgo
  architectures. (#68)
- Switching workspaces restores the previously active session;
  `turn_end` carries the authoritative turn duration. (#77, #78)

## [0.1.0] - 2026-09-01

First release of the opencraft desktop workbench: a local-first workflow
runner built on flowcraft core v0.2.2, delivered as a macOS/Linux/Windows
desktop app (Wails v2 + React).

### Added

- Closing the window now keeps the app running in the background by
  default: the process hides to the system tray (Windows/Linux) or to
  the menu bar (macOS, native `NSApp hide` semantics) instead of
  quitting. A new "Interface" settings toggle switches between
  "Minimize to tray" and "Quit". The tray icon menu offers Show and
  Quit, and launching the app again while it is backgrounded restores
  the main window instead of starting a second instance (single-instance
  lock on Windows/macOS/Linux). On macOS, clicking the Dock icon brings
  the hidden window back.
- Tool results that contain git diffs (e.g. `read_file` on a `.diff`
  artifact, including compacted or legacy sessions whose result JSON
  was stored with broken escaping) now render as proper git diff cards
  instead of raw JSON text. A regression test pins the `read_file`
  result serialization so the escaping cannot silently break again.
- Windows desktop builds: the execd socket umask now lives behind a
  platform-specific file (`syscall.Umask` is unix-only), a
  `build/windows/icon.ico` resource, and CI/release jobs that produce
  `opencraft-<version>-windows-amd64.zip` plus an NSIS installer
  (`opencraft-<version>-windows-amd64-installer.exe`).
- Optional macOS code signing and notarization in the release workflow
  (activates automatically when the Apple secrets are configured; supports
  App Store Connect API-key auth, writes a verification summary artifact,
  and marks pre-release tags).
- Homebrew cask (`Casks/opencraft.rb`) and a universal (arm64 + amd64) macOS
  dmg for releases.
- Release infrastructure: GitHub Actions release workflow (tag `v*`) that
  builds and publishes the macOS app bundle and the Linux binary with the
  version injected via `-ldflags`.
- `LICENSE` (MIT) and this changelog.
- **Desktop UI** — chat with streaming reasoning / tool-call / output blocks,
  interrupt and cancel, session list (resume, rename, export, delete),
  workspace switching, subagent kanban and run sidebar, graph editor,
  diagnostics tab, settings pages, full i18n (en/zh).
- **Inference** — one or more instances per provider (openai, anthropic,
  azure, bytedance, deepseek, kimi, minimax, qwen) with custom endpoints,
  model and capability selection, and a router priority list with retry
  fallback. Per-model usage is recorded across workspaces and sessions and
  shown as hourly/daily trend charts.
- **Tools** — file group (read_file/write_file/list_dir/grep/glob),
  exec_command (shell), exec_session (PTY/resize/signal/timeout), apply_patch,
  web_fetch, ask_user, update_plan, request_permissions,
  skill_search/skill_read/skill_install/skill_create/skill_modify, and hidden
  auto-compaction. Tool results are protected by a middleware chain: truncate
  cache, 32k hard cap, secret redaction, and a JSONL audit trail.
- **Runtime** — local execd JSON-RPC (stdio + unix socket, self-fork, parent
  death cleanup), project-scoped SQLite session store, buffer-fold memory
  summary, AGENTS.md worldstate with git context, layered config (embedded +
  `~/.opencraft/config/` + project `.opencraft/config/`), and seatbelt/bwrap
  sandbox with project `.opencraft/approvals.yaml` approvals.
- **Agents** — persistent subagents via create_agent/update_agent/
  unregister_agent, delegation with kanban overview, and external lifecycle
  hooks (Pre/PostToolUse, UserPromptSubmit, PermissionRequest, TurnEnd,
  SessionStart/End, SubagentStart/Stop).
- **Skills** — discovery, BM25 search/read, git-based install with subpath
  containment, authoring (create/modify), and built-in
  skill-creator/skill-installer/code-review skills.
- **Network policy** — configurable exec sandbox netpolicy (default/deny-all/
  allow-list/proxy) and a web_fetch SSRF gate blocking private/loopback/
  link-local destinations by default.
- **Session events** — append-only JSONL rollout stream per session with
  scrubbed user input and redacted audit records.
### Changed

- App icon artwork now follows Apple's icon grid: the 1024px canvas
  keeps ~100px transparent margins (824px artwork) instead of painting
  edge-to-edge, so the Dock/desktop/Explorer no longer render the icon
  larger than standard apps. `build/windows/icon.ico` was regenerated
  from the updated artwork.
- flowcraft core upgraded to v0.2.2. Windows now uses flowcraft's
  Windows sandbox backend with OS-level write confinement (restricted
  Low-integrity token: children can only write inside the workspace and
  configured writable paths) plus job-object process-tree lifecycle and
  resource caps, instead of the no-isolation local runner. Sandboxed
  process trees are terminated with their jobs (`KILL_ON_JOB_CLOSE`).
  Interactive `exec_session` is disabled on Windows (the backend does
  not combine confinement with ConPTY yet), and the `pty` capability is
  no longer advertised there.
- Windows execd lifecycle aligned with Unix: shutdown closes the client
  connection first so the child runs its own session cleanup (SIGTERM
  is not deliverable on Windows), and the parent-death watchdog waits
  on a handle to the parent process instead of relying on orphan
  reparenting.
- `EnvironmentInfo` now reports the platform shell (`cmd.exe` on
  Windows, `/bin/sh` elsewhere) instead of a hardcoded `/bin/sh`.
- Assistant system prompt persona updated to a work partner — helping with
  coding and any local workflow, matching the README positioning.
- Linux desktop builds now target webkit2gtk-4.1 (`-tags webkit2_41`), matching
  the Ubuntu 24.04 CI runner.
- Cleaned up all `golangci-lint` findings (errcheck/ineffassign/staticcheck/
  unused) so the CI lint gate is green.

### Security

- Session IDs validated at bindings and store layer to prevent path escape.
- Workspace-bounded file bindings with symlink containment.
- Atomic 0600 writes for user configuration and secrets.
- Fail-closed permission approval and read-only session mode with
  safe-command auto-approval.
- Execd unix sockets created user-only from the start.
