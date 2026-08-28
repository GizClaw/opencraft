# Hello Plugin

The reference plugin for the OpenCraft frontend plugin host (Phase 0).
It contributes one settings panel and one sidebar entry; clicking the
sidebar entry flashes a greeting.

## Install

```sh
mkdir -p ~/.opencraft/plugins
cp -R plugins/hello ~/.opencraft/plugins/hello
```

Then open OpenCraft → Settings → Plugins and press Refresh, or restart
the app. The plugin is enabled by default; disable it from the same
page.

## Bundle contract

`dist/index.js` is evaluated by the host as `new Function('ctx', src)`.
The `ctx` object exposes:

- `ctx.React` — the host React runtime for building components;
- `ctx.contribute({ settingsPanels?, sidebarEntries? })` — declares
  contribution points;
- `ctx.ui.flash(text)` — shows a transient status message.

See `docs/plans/plugin-system.md` for the full design.
