# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
