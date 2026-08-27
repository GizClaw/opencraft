# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Optional macOS code signing and notarization in the release workflow
  (activates automatically when the Apple secrets are configured).
- Homebrew cask (`Casks/opencraft.rb`) and a universal (arm64 + amd64) macOS
  dmg for releases.
- Release infrastructure: GitHub Actions release workflow (tag `v*`) that
  builds and publishes the macOS app bundle and the Linux binary with the
  version injected via `-ldflags`.
- `LICENSE` (MIT) and this changelog.

### Changed

- Assistant system prompt persona updated to a work partner — helping with
  coding and any local workflow, matching the README positioning.
- Linux desktop builds now target webkit2gtk-4.1 (`-tags webkit2_41`), matching
  the Ubuntu 24.04 CI runner.
- Cleaned up all `golangci-lint` findings (errcheck/ineffassign/staticcheck/
  unused) so the CI lint gate is green.

## [0.1.0] - 2026-08-27

First release of the opencraft desktop workbench: a local-first workflow
runner built on flowcraft core v0.1.32, delivered as a macOS/Linux desktop
app (Wails v2 + React).

### Added

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
- **Turn-level undo/redo** — per-turn file snapshots in git workspaces with
  UI rollback.

### Security

- Session IDs validated at bindings and store layer to prevent path escape.
- Workspace-bounded file bindings with symlink containment.
- Atomic 0600 writes for user configuration and secrets.
- Fail-closed permission approval and read-only session mode with
  safe-command auto-approval.
- Execd unix sockets created user-only from the start.
