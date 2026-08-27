# opencraft

A Go coding agent built on [flowcraft](https://github.com/GizClaw/flowcraft)
(`core v0.1.32`), delivered as a macOS/Linux desktop workbench (Wails v2 +
React). The agent reads/writes/edits files, runs shell commands with PTY,
approves permissions, persists and resumes sessions — driven by an LLM
through flowcraft's config-driven graph engine.

## Status

P0 core loop is closed:

- **Desktop UI** — chat with streaming reasoning / tool-call / output
  blocks, interrupt/cancel, session list (resume, rename, export,
  delete), workspace switching, kanban view of subagent delegation,
  and settings for inference instances, MCP servers, permissions, and
  skills;
- **Inference** — one or more instances per provider with custom
  endpoints, model and capability selection, and a router priority
  list with retry fallback. Usage is recorded per model across every
  workspace and session (`~/.opencraft/user.db`) and shown as
  hourly/daily trend charts;
- **Tools** — file group (read_file/write_file/list_dir/grep/glob),
  exec_command (shell), exec_session (PTY/resize/signal/timeout),
  apply_patch, web_fetch, ask_user, update_plan, request_permissions,
  skill_search/skill_read/skill_install/skill_create/skill_modify,
  hidden auto-compaction; result protection middleware chain (truncate
  cache + 32k hard cap + secret redaction + JSONL audit trail);
- **Runtime** — local execd JSON-RPC (stdio + unix socket, self-fork,
  parent death cleanup), project-scoped SQLite session store, buffer-fold
  memory summary, AGENTS.md worldstate, layered config (embedded +
  `~/.opencraft/config/` + project `.opencraft/config/`), seatbelt/bwrap
  sandbox with project `.opencraft/approvals.yaml` approvals.

See `docs/02-codex-gaps.md` for the full gap baseline against codex-rs.

## Quick start

```sh
make fmt lint test                          # fmt + lint + go test
wails dev                                   # run the desktop app (hot reload)
wails build -platform darwin/arm64          # package the macOS arm64 app
```

First launch opens the desktop workbench; when inference is not
configured yet the chat area guides you to the settings page, which
writes `~/.opencraft/config/opencraft.yaml` (the single user-editable
document: inference instances, router policy, MCP servers, …).

Requires Go 1.25.5+ and Node 22 (frontend). macOS arm64 (seatbelt) and
Linux (bwrap) sandboxing are supported; Windows is explicitly out of
scope.

## Releasing

Tag a release and push it; the [release workflow](.github/workflows/release.yml)
builds the macOS app bundle and the Linux binary, injects the version into the
binary via `-ldflags`, and publishes a GitHub Release with notes taken from
[CHANGELOG.md](CHANGELOG.md):

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Layout

- `main.go` — Wails desktop entry; `execd_main.go` — internal sandbox
  child mode (`opencraft execd`)
- `internal/desktop` — app shell, Go bindings, settings / workspaces /
  sessions / usage / agents views
- `internal/app` — runtime assembly, exec policy, sandbox pm, worldstate
- `internal/config` — layered config (embed template + user + project
  merge), inference instances, MCP servers
- `internal/execd` — local exec server (JSON-RPC over stdio/unix socket)
- `internal/sessions` — project session store (SQLite `session.db` +
  per-session JSON history/usage/permissions/plans)
- `internal/agents` — subagent lifecycle and delegation
- `internal/skills` — skill discovery
- `internal/memory` — buffer-fold memory summary + session archive
- `internal/sandbox` — seatbelt/bwrap sandbox
- `internal/tools` — tool source factories plus the middleware assembly
  (applypatch/askuser/assembly/exec/files/plan/requestpermissions/
  webfetch)
- `internal/usage` — user-level usage database (`~/.opencraft/user.db`)
- `internal/utils` — resourcedep + ported extract/httpkit helpers
- `frontend/` — React/TypeScript UI (chat, sessions, settings, usage)
- `docs/` — design docs, codex-rs gap analysis, code review reports

## Version facts

Pinned flowcraft modules (see `go.mod`): `core v0.1.32`, `driver/*`
v0.1.6–v0.1.11 (8 providers: openai v0.1.8, anthropic v0.1.6, azure
v0.1.9, bytedance v0.1.11, deepseek v0.1.6, kimi v0.1.6, minimax
v0.1.6, qwen v0.1.6). The upstream `sdk/sdkx v0.5.x` release naming is
obsolete; current module scheme is `core` / `driver/*`.
