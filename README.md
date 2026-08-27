# opencraft

A Go coding agent built on [flowcraft](https://github.com/GizClaw/flowcraft)
(`core v0.1.32`), delivered as a macOS/Linux desktop workbench (Wails v2 +
React). The agent reads/writes/edits files, runs shell commands with PTY,
approves permissions, persists and resumes sessions — driven by an LLM
through flowcraft's config-driven graph engine.

[![CI](https://github.com/GizClaw/opencraft/actions/workflows/ci.yml/badge.svg)](https://github.com/GizClaw/opencraft/actions/workflows/ci.yml) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

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
- **Workflow** — git context injected into the worldstate (branch, status,
  diffstat, bounded diff), turn-level undo/redo with UI rollback,
  append-only JSONL session rollout stream, external lifecycle hooks
  (Pre/PostToolUse, UserPromptSubmit, PermissionRequest, TurnEnd, session
  and subagent events), and a diagnostics tab (env report, sandbox probe,
  policy check);
- **Network policy** — configurable exec sandbox netpolicy (default /
  deny-all / allow-list / proxy) and a web_fetch SSRF gate that blocks
  private, loopback, and link-local destinations by default.

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

## Version facts

Pinned flowcraft modules (see `go.mod`): `core v0.1.32`, `driver/*`
v0.1.6–v0.1.11 (8 providers: openai v0.1.8, anthropic v0.1.6, azure
v0.1.9, bytedance v0.1.11, deepseek v0.1.6, kimi v0.1.6, minimax
v0.1.6, qwen v0.1.6). The upstream `sdk/sdkx v0.5.x` release naming is
obsolete; current module scheme is `core` / `driver/*`.

## License

Released under the [MIT License](LICENSE).
