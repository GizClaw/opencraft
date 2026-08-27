<p align="center">
  <img src="build/appicon.png" alt="opencraft" width="128">
</p>

<h1 align="center">opencraft</h1>

<p align="center">
  A local-first coding agent desktop workbench built on
  <a href="https://github.com/GizClaw/flowcraft">flowcraft</a>.
</p>

<p align="center">
  <a href="https://github.com/GizClaw/opencraft/actions/workflows/ci.yml">
    <img src="https://github.com/GizClaw/opencraft/actions/workflows/ci.yml/badge.svg" alt="CI">
  </a>
  <a href="LICENSE">
    <img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT">
  </a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey" alt="Platforms: macOS / Linux">
</p>

opencraft is a Go coding agent that reads and edits files, runs shell
commands with approval, and persists and resumes sessions — all inside a
macOS/Linux desktop workbench (Wails v2 + React). It is driven by an LLM
through flowcraft's config-driven graph engine, with a local execd sandbox,
SQLite session store, and per-project approval policy.

## Features

- **Chat & sessions** — streaming reasoning, tool calls, and output blocks;
  interrupt and cancel; session resume, rename, export, and delete; kanban
  view of subagent delegation; i18n (English / 中文).
- **Inference** — one or more instances per provider (OpenAI, Anthropic,
  Azure, ByteDance, DeepSeek, Kimi, MiniMax, Qwen) with custom endpoints,
  router priority with retry fallback, and per-model usage trends.
- **Tools & safety** — file group, exec_command, exec_session (PTY),
  apply_patch, web_fetch, update_plan, request_permissions, and skill tools,
  protected by a middleware chain: truncation cache, 32k result cap, secret
  redaction, and a JSONL audit trail.
- **Runtime & sandbox** — local execd (stdio + unix socket, self-fork,
  parent-death cleanup), project-scoped SQLite store, buffer-fold memory
  summary, AGENTS.md worldstate, layered config, and seatbelt/bwrap sandbox
  with `.opencraft/approvals.yaml` approvals.
- **Multi-agent & skills** — persistent subagents with delegation kanban;
  skill discovery, git-based install, and authoring tools.
- **Workflow** — git context in the worldstate, turn-level undo/redo, JSONL
  session rollout stream, external lifecycle hooks, configurable network
  policy with a web_fetch SSRF gate, and a diagnostics tab.

## Installation

Download the latest package from the
[Releases](https://github.com/GizClaw/opencraft/releases) page:

- `opencraft-<version>-macos-arm64.zip` — macOS (Apple Silicon)
- `opencraft-<version>-linux-amd64.tar.gz` — Linux (x86_64)

Windows is explicitly out of scope. Release binaries are built from tagged
commits by the [release workflow](.github/workflows/release.yml).

## Build from source

Prerequisites: Go 1.25.5+, Node 22, and the
[Wails v2 prerequisites](https://wails.io/docs/gettingstarted/installation)
for your platform.

```sh
make fmt lint test                          # fmt + lint + go test
wails dev                                   # run the desktop app (hot reload)
wails build -platform darwin/arm64          # package the macOS arm64 app
wails build -platform linux/amd64 -tags webkit2_41   # package the Linux app
```

On first launch the desktop workbench guides you to the settings page when
inference is not configured yet. Settings are written to
`~/.opencraft/config/opencraft.yaml` — the single user-editable document for
inference instances, router policy, and MCP servers.

## Documentation

Release history is tracked in [CHANGELOG.md](CHANGELOG.md).

## Contributing

Contributions are welcome. Please open a pull request; the CI workflow
(fmt, lint, build, race tests, macOS/Linux packaging) and a review are
required before merging to `main`.

Maintainers release by tagging a version:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The tag triggers the release workflow, which builds the packages, injects
the version into the binary via `-ldflags`, and publishes the GitHub
Release with notes from the changelog.

## License

Released under the [MIT License](LICENSE).
