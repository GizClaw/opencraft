# Build assets (Wails v3)

This directory holds the wails3 build configuration and platform assets.

* `config.yml` — product metadata (bundle id, product name, version,
  copyright) used to generate platform assets.
* `Taskfile.yml` — shared tasks (frontend build, bindings, icons,
  docker/server helpers).
* `darwin/`, `linux/`, `windows/` — platform Taskfiles consumed by the root
  `Taskfile.yml`. Desktop only: no iOS/android targets are maintained.
* `appicon.png`/`appicon.svg` — source artwork; icons are generated into the
  platform dirs by `wails3 generate icons` during every build.

After editing `config.yml`, regenerate the derived assets with:

```sh
wails3 task common:update:build-assets
```

Build and package through the root Taskfile:

```sh
wails3 task build        # production binary for the host OS
wails3 task package      # .app / NSIS / Linux package for the host OS
wails3 task dev          # dev app + vite hot reload
```
