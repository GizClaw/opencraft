# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- The built-in `code-review` skill was removed; review behavior is left
  to installed user/repo skills and the agent's review guidance.
- flowcraft upgraded to core v0.2.3 and drivers v0.2.1. Reasoning
  capabilities now use the new `ReasoningCapability` object (kind +
  canonical-to-wire effort map) end to end: config writes the object
  form, the settings page edits each model's canonical-to-wire effort
  map, and OpenAI/Azure `effort_none` is editable there too. Thinking
  effort gains the canonical `minimal` and `xhigh` levels in chat and
  automations.
- The inference settings page now offers each driver's built-in model
  catalog as a combobox: picking a built-in prefills the model's
  capabilities (inputs/outputs, reasoning kind + effort map,
  dimensions, effort_none), with a one-click reset to catalog
  defaults; free-form custom model names stay supported. Deprecated
  catalog models are marked with ⚠️ and their replacement. Provider
  default models now come from the first non-deprecated catalog entry
  instead of hardcoded names.
- Desktop exit polish: real quits (tray Quit, macOS Cmd+Q/Dock Quit,
  and the "Quit" close mode) now ask for confirmation first because
  exiting stops scheduled tasks. The tray/menu-bar icon uses the
  full-colour app icon on every platform, and the tray menu shows the
  app version and a short about line. Native tray/exit copy follows
  the UI's zh/en language, synced from the frontend and persisted in
  desktop preferences.

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
