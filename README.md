# opencraft

A Go coding agent built on [flowcraft](https://github.com/GizClaw/flowcraft)
(`core v0.1.10`). Interactive coding agent: read/write/edit files, run shell
commands with PTY, approve permissions, persist and resume sessions — driven by
an LLM through flowcraft's config-driven graph engine.

## Status

P0 core loop is closed:

- **TUI** streaming conversation, tool cards, ask_user modal, interrupt/cancel,
  `/resume` session switching;
- **Tools**: file group (read_file/write_file/list_dir/grep/glob),
  exec_command (shell), exec_session (PTY/resize/signal/timeout),
  apply_patch, web_fetch, ask_user, update_plan, request_permissions;
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
go run ./cmd/opencraft execd   # JSON-RPC exec server (stdio + unix socket)
```

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
- `internal/state` — SQLite state resource (`<project>/.opencraft/sessions/session.db`)
- `internal/tools` — tool source factories (applypatch/askuser/exec/execsession/files/plan/requestpermissions/webfetch)
- `internal/tui` — bubbletea TUI
- `internal/utils` — resourcedep + ported extract/httpkit helpers
- `docs/` — design docs & codex-rs gap analysis (committed)

## Version facts

Pinned flowcraft modules (see `go.mod`): `core v0.1.10`, `driver/* v0.1.2`
(8 providers: openai/anthropic/azure/bytedance/deepseek/kimi/minimax/qwen),
`backends/checkpoint v0.1.0`. The upstream `sdk/sdkx v0.5.x` release naming is
obsolete; current module scheme is `core` / `driver/*` / `backends/checkpoint`.
