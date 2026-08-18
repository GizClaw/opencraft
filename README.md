# opencraft

A Go coding agent built on [flowcraft](https://github.com/GizClaw/flowcraft)
(`core v0.1.13`). Interactive coding agent: read/write/edit files, run shell
commands with PTY, approve permissions, persist and resume sessions — driven by
an LLM through flowcraft's config-driven graph engine.

## Status

P0 core loop is closed:

- **First-run setup** interactive wizard: the fixed inference wiring
  (all provider resources + infer assembly + router retry policy) is
  embedded; the wizard writes the variable parts — the API keys you
  have and the router's target list (priority order, falls back to the
  next keyed provider on failure) — to
  `~/.opencraft/config/opencraft.yaml`;
- **TUI** streaming conversation, tool cards, ask_user modal, interrupt/cancel,
  `/resume` session switching, `/permissions` sandbox mode, `/skills` picker,
  status-line token usage (in/out/cache/think/CHR);
- **Tools**: file group (read_file/write_file/list_dir/grep/glob),
  exec_command (shell), exec_session (PTY/resize/signal/timeout),
  apply_patch, web_fetch, ask_user, update_plan, request_permissions,
  skill_search/skill_read/skill_install, hidden auto-compaction;
- **Runtime**: local execd JSON-RPC (stdio + unix socket, self-fork, parent
  death cleanup), SQLite session store (threads/turns/items/summary_nodes),
  buffer-fold memory summary, AGENTS.md worldstate, layered config
  (embedded + `~/.opencraft/config/` + project `.opencraft/config/`),
  seatbelt/bwrap sandbox with project `.opencraft/approvals.yaml` approvals.

See `docs/02-codex-gaps.md` for the full gap baseline against codex-rs.

## Quick start

```sh
make fmt lint test             # fmt + golangci-lint/staticcheck + go test
go run ./cmd/opencraft         # interactive TUI (default subcommand)
go run ./cmd/opencraft setup   # re-run the inference configuration wizard
go run ./cmd/opencraft execd   # JSON-RPC exec server (stdio + unix socket)
```

First launch opens the configuration wizard before the chat TUI; it
writes `~/.opencraft/config/opencraft.yaml` (the single user-editable
document: inference wiring, sandbox policy, MCP servers, …).

Requires Go 1.26+. macOS arm64 (seatbelt) and Linux (bwrap) sandboxing are
supported; Windows is explicitly out of scope.

## Layout

- `cmd/opencraft` — CLI entrypoints (`run`, `execd`)
- `internal/app` — runtime assembly, exec policy, sandbox pm, worldstate
- `internal/config` — layered config (embed template + user + project merge)
- `internal/execd` — local exec server (JSON-RPC over stdio/unix socket)
- `internal/interact` — approval broker / ask-user flow
- `internal/memory` — buffer-fold memory summary + session archive
- `internal/sessions` — JSON session history store (`<project>/.opencraft/sessions/<id>/history/*.json`)
- `internal/setup` — first-run inference wizard (all providers registered, key + auto router)
- `internal/state` — SQLite state resource (`<project>/.opencraft/sessions/session.db`)
- `internal/tools` — tool source factories (applypatch/askuser/exec/execsession/files/plan/requestpermissions/webfetch)
- `internal/tui` — bubbletea TUI
- `internal/utils` — resourcedep + ported extract/httpkit helpers
- `docs/` — design docs & codex-rs gap analysis (local, not tracked)

## Version facts

Pinned flowcraft modules (see `go.mod`): `core v0.1.13`, `driver/* v0.1.3`
(8 providers: openai/anthropic/azure/bytedance/deepseek/kimi/minimax/qwen),
`backends/checkpoint v0.1.0`. The upstream `sdk/sdkx v0.5.x` release naming is
obsolete; current module scheme is `core` / `driver/*` / `backends/checkpoint`.
