# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- The desktop shell is on Wails v3.0.0-beta.26, from beta.17 — the bump lands
  in the three places that pin it (go.mod, `@wailsio/runtime`, the CI and
  release `wails3` installs). What it buys: `wails3 dev` now waits for the vite
  server to answer HTTP 200 before starting the app, so the first paint no
  longer races the dev server, and it stops the frontend when the app exits
  instead of leaving an orphan holding the port; SIGINT/SIGTERM reach the
  app's shutdown hooks instead of being a bare kill that skips them; Windows
  recovers from a WebView2 process failure by rebuilding the controller
  instead of leaving a permanently blank window, and no longer ships the
  bundled WebView2Loader DLL; on Linux a closing window stops its in-flight
  loads and single-instance handling is stricter. The darwin build now passes
  `-tags private_mac_apis`, which is required for the pet window to be
  transparent — without it the window silently stays opaque — at the cost of
  reading one undocumented WebKit property.

### Fixed

- A deleted conversation stays deleted. Deleting one retires its id in
  the same transaction that removes its rows (`deleted_conversations`,
  workspace migration 021), and every writer that could recreate the
  conversation refuses a retired id: the transcript append and the
  start-title seed, the metadata upsert, the conversation-state
  documents, session settings, and memory fold nodes. The writers that
  outlive a turn are the ones that bit — a detached review, a fold
  condensing in the background, a delegation note reflowing after the
  subagent finished — and they are the reason the in-process checks
  (auto title, delegation reflow) are a fast path in front of the store
  rather than the fix itself: "deleted" now holds across restarts, for
  writers this process never sees. A one-way migration step (022)
  removes the zero-turn, usage-only `(empty)` rows older builds had
  already resurrected and retires their ids, so an upgrade does not ask
  for a second delete.
- The usage writers no longer mint the conversation they write to.
  `AddUsage` and `RecordUsageIfEmpty` (and `RecordUsage`, the verbatim
  overwrite with no production caller) used to read the row and, when
  the read missed, create it and write the totals back through an
  upsert — so a usage call landing after the delete rebuilt the row it
  was writing to as an `(empty)` entry that the sidebar kept listing.
  They now persist through `state.UpdateConversationUsage`, an UPDATE
  that never inserts (`conversations.usage_json` is a cache on a row the
  transcript owns); a row that is gone when the write lands is a skip,
  not a rebuild. `RecordUsageIfEmpty` reports which of the three
  outcomes it was — written, already accounted for, or the row was gone
  — so an import whose conversation was deleted while it settled still
  forwards the bundle's spent tokens to the user-level ledger instead of
  dropping them or counting them twice.
- The repeat delete purges whatever came back under a deleted id and
  still reports success — the caller asked for "this conversation is
  gone", and the rows were already gone the first time. A listing that
  raced the delete is filtered by the same tombstone, so the sidebar
  cannot show an entry that every path into it refuses. (#218)

## [0.6.0] - 2026-09-25

### Added

- Settings ▸ Interface gains **Accent colour**: five presets — blue (the
  default), violet, teal, orange, rose — each a rung per theme, so a
  theme flip repaints the accent, the swatches and the focus rings
  without a component knowing. The id is written as `data-accent` on
  documentElement, the rungs live in `style.css`, and the ids are a
  contract with `core/ui_prefs.go` — an unknown id falls back to the
  shipped accent. (#216)
- The keyboard is one table with one place to read it: every key the
  shell owns lives in `frontend/src/lib/keys.ts` (the combo, whether
  it runs in a text field, under an overlay, or repeats) with one
  capture-phase dispatcher, and ⌘/ opens the reference from the same
  table. New bindings: Esc or ⌘. stops a reply, ⌘L the composer,
  ⌘↑/⌘↓ walk the transcript, ⌘1–⌘4 resume a session, ⌘[ / ⌘] move
  sessions and ⌘⇧[ / ⌘⇧] workspaces, ⌘⇧G toggles Git, ⌘⇧T cycles the
  theme; session digits are the sidebar's own numbering, shown while
  the modifier that runs them is held. An input method owns the
  keyboard through `lib/ime.ts`, which covers what the flag alone does
  not — Chromium and WebView2 re-deliver the key that *ends* a
  composition after `compositionend`, and the guard arms only for a
  keydown the composition itself reported, so an ordinary keydown can
  no longer disarm it and commit Enter to the palette or the composer.
  (#205, #215)
- macOS has a menu bar the app owns, built from a table
  (`internal/adapters/desktop/menuspec.go`): every item localized, the
  platform's rows kept as roles, and the app's commands as bridged
  rows — the item carries the accelerator and a click emits the
  frontend's shortcut id over `opencraft:menu`, so the menu runs the
  same command table as the palette and the sheet and cannot drift. A
  test holds the Go table to the TypeScript one. (#205)
- Past sessions are searchable by the assistant: `session_search` runs
  FTS5 full-text recall (trigram tokenizer, so a substring inside
  Chinese matches) over this workspace's archived turns. The index
  stores the prompt projection, is written in the archive's own
  transaction, and older databases backfill once off the open path;
  queries under three characters fall back to a substring scan, and
  hits de-duplicate per conversation with a marked snippet. (#204)
- The assistant can remember facts across sessions: a user-level store
  (`user.db`) holds them with the workspace and conversation they came
  from. The `remember` tool confirms before writing and applies the
  same secret rules as tool results; dedupe, caps and provenance live
  in the store, so the tool, the settings card and an accepted review
  suggestion all write through one path. Live facts enter the top of
  every turn, and Settings ▸ Memory browses, edits and deletes them.
  (#204)
- A post-turn write-back review proposes facts worth keeping: off by
  default, one inference call per turn that meets its cadence and
  tool-use threshold returns strict JSON candidates that wait in the
  settings card's pending queue, and accepting one writes through the
  same user-memory path. (#204)
- Skills track how they are used and can be retired without losing
  anything: uses, pins and retirement state show on the skills page,
  the curator marks idle rarely-used skills as candidates, and
  retiring snapshots a skill to a tar.gz under the app home. Builtin
  skills are never candidates and no file is deleted. (#204)
- Delegated runs are visible end to end: the desktop supplies the
  stream resolver and exporter core's delegation service takes, so an
  async run's deltas land in the conversation that asked for it and
  its result flows back once (idempotent by child run). The SubagentDock
  nests a delegated run under its parent, and Settings ▸ Tools gained
  a delegation card — concurrency and depth limits plus allow/block
  target lists. (#204)
- Mid-turn steering: a message submitted while a turn runs reaches the
  model at the next tool-round boundary (Enter sends it; barge-in
  moves to Cmd/Ctrl+Enter beside a stop button), several waiting
  corrections arrive as one message, at most one 32 KiB steer per
  boundary, and an over-large submit is refused with its text kept.
  Each interjection stays where it was typed with its state — waiting,
  taken, or undelivered — with resend, copy and discard. (#181)
- Automations get a per-task run limit and a stop. `timeout` bounds one
  run in whole minutes (empty = the 15-minute default, at most 24h): a
  run past it is cancelled and its record says `timeout`; a live
  scheduled run can be stopped — its record reads `canceled`, and the
  failure notification stays quiet for a stop the user asked for. The
  editor gained a run-limit field and the run list a timeout badge.
  (#181, #186)
- A reply the process never finished is no longer lost: each completed
  wave writes one checkpoint, and the next assembly turns a checkpoint
  with no archive row into an interrupted turn — same conversation and
  run id, the original message plus what the crash produced, archived
  and folded in one transaction. The transcript offers **Continue**
  and **Edit & resend** on the newest one, and Settings ▸ Diagnostics
  gained a Crash recovery card. (#188)
- Settings ▸ Diagnostics gained a Provider round-trip probe (DEV
  tools): it wraps the process HTTP transport, so every provider call
  leaves two records — request left, response headers — separating
  network and provider time-to-first-byte from local assembly. The
  switch is `desktop.json`'s `diagnostics.httpProbe`, and it parks
  itself while an HTTP MCP server is configured. (#189)
- A second instance can run next to the installed app: `--profile
  <name>` keeps the shared app home and moves the state root to
  `~/.opencraft-<name>`, with `--data-dir` / `--app-home` /
  `--config-dir` for the other combinations; the deploy document
  resolves `${ocraft:APP_HOME}` while `${ocraft:DATA_DIR}` keeps state
  semantics. Window title, tray tooltip and a Diagnostics card name
  the profile. (#190)
- A delegated subagent's report is filed as the app's own turn:
  `archive_turns.kind` names the writer and `payload_json` holds its
  record (migration 018 parses older notes once). Every reader goes by
  the kind — title derivation, fork and import naming, and the
  transcript, which draws a note card instead of a user bubble — and a
  note landing while its conversation is open is folded in by seq.
  (#209)
- Diagnostics measures the app the way the machine does: "The app's
  processes" charts `proc.mem.family_total` and `proc.mem.footprint`
  split by role — `self`, `child` and the platform's `webcontent` /
  `gpu` / `networking` / `helper` processes — and a renderer probe
  reports what loaded transcripts hold (`store_media_bytes`,
  `store_text_bytes`, `loaded_convs`, `dom_images`). (#211, #214)
- The command palette is rebuilt from live state on every open and
  ranks through a pure function (`lib/commands.ts`), so it cannot
  point at a workspace, session or settings tab that no longer exists.
  It runs on the shared overlay stack, and the ⌘K shortcut is captured
  on the window so it works while the composer has focus. Settings is
  searchable through a static index (`settingsIndex.ts`) naming each
  card's tab and anchor, since only the active tab is mounted. (#174)
- The tool transcript says what each call did, not what it was called:
  every tool gets one summary line (`toolTarget` plus a verb table,
  shared with the collapsed group header), a duration from when the
  call and its result arrived (archive replays stay undated), and a
  shared collapsed window for long output. `exec_session` renders a
  terminal block keyed by action, `view_image` shows the frame the
  model saw, and a `read_file` result names the line window it came
  from. (#176)
- The composer can step out of a markdown wrapper: Shift+Enter on an
  empty line inside a list, quote or heading moves the caret to a
  plain paragraph after it, and Backspace at the start of a wrapped
  line lifts the formatting while keeping the text. Usage numbers
  render on one unit ladder (`lib/compactNumber.ts`, three significant
  digits, k → E), shared by the hero, chart axes, settings and the
  sidebar. (#176)
- The Files pane marks git changes: `Git.FileMarks` answers one
  bounded, read-only query per file with structured line ranges (base
  `git diff HEAD -U0` — the working tree, never the index alone). The
  preview draws a gutter (insertions, replaced lines, deletion wedges)
  and a chip that hands the file to the Git panel; the tree carries
  the same vocabulary as badges, with folders rolling their contents
  up. The viewer re-reads a file when its mtime moves, and
  `desktop.json`'s `gitMarks` switches it off (absent means on).
  (#198)
- The chat header is a 44px two-tier strip — the name and viewer
  toggle on top; the sandbox policy, the session's turns and tokens,
  and the live run's stage and in-flight clock underneath — with an
  accent sweep along its bottom hairline while a turn runs. The
  sidebar seam became a hairline owned by a `SidebarResizeHandle`:
  pointer capture, arrows to nudge, double-click to reset, one storage
  write per gesture, and `role="separator"` reporting its range.
  (#179)
- The UI owns one component and scale layer instead of hand-written
  values: `components/ui` carries Button, IconButton, Input, Badge,
  Modal, SaveBar and Segmented, the ladders for text, radius, elevation
  and icons live in `style.css` and `ui/icon.ts`, and a contract test
  fails a raw rem literal, a Tailwind radius or shadow utility and a
  default-palette colour, so the ladders stay the only vocabulary.
  `npm run audit:ui` renders the real frontend against the mock backend
  and writes one screenshot per surface in both themes, so a design
  change can be reviewed as an image. An interaction prompt says how
  much it matters (`foundation/interact` gains `Severity` — info,
  notice, danger — defaulting from the prompt kind and falling back on
  an unknown value, so a typo cannot downgrade a danger prompt; the
  producers tag their own, sandbox escalation danger,
  `request_permissions` and `confirm` notice, `ask_user` info, and
  `InteractionCard` is the one place mapping it to chrome), and a
  clicked link goes through one classifier (`lib/linkTarget.ts`): a URL
  scheme reaches the system browser, a Windows drive path stays local,
  an empty target and a `#anchor` are ignored, and everything else
  resolves under the backend read roots into the caller's handler —
  chat rail tab, dialog page, system file manager. (#161)
- The workspace panel lists hidden files when asked to: the eye beside
  the search box persists `ui.showHiddenFiles` (off by default, and a
  failed save rolls the applied value back), `File.List` and
  `File.Search` take the decision from the caller so each surface owns
  its own scope, and "hidden" is the platform rule — a leading dot on
  Unix, plus the hidden/system attribute on Windows, where `.venv` and
  `.next` carry none — with `.git` still skipped unconditionally and
  `list_dir` and `grep` reading the same helper. The composer no
  longer paints a scrim or a mask: the transcript runs the full column,
  the card covers what slides behind it, and the wrapper stays
  click-through so the wheel and the margins still reach the scroller.
  (#162)

### Changed

- A conversation's history is one transcript; the model's window is a
  read-time projection of it (`capabilities/memory/projection.go`),
  and a message's identity is its transcript coordinate
  `(conversation_id, seq)` rather than a hash of the position it was
  loaded at, so a skipped row cannot shift everything after it.
  `memory_items` is dropped (migration 020, one-way), import is ready
  when the transcript lands, and fork and import no longer seed
  memory; a text-less turn enters the window as a placeholder naming
  what it carries. (#210)
- A conversation's settings — reasoning effort, model hint, sandbox
  permission mode — are one `conversation_state` document (`settings`;
  migration 019). The API is unchanged, the read-modify-write is one
  transaction, and "never set" and "set to empty" stay the same thing.
  (#210)
- One name per thing across the session layer: document names in one
  registry, an undecodable document comes back as a
  `*state.CorruptDocumentError` naming its row, and every UI event
  name has one home per side, held by a Go test that parses the
  TypeScript map. (#212)
- flowcraft core moves to v0.4.8 / v0.4.7: sandbox sessions spawn with
  the signal mask cleared (so `Session.Signal(Interrupt)` ends the
  process), Walk and Glob emit slash-separated paths on every
  platform, and the narrow board reads arrived (`ChannelLen`,
  `LastMessage`, `ChannelTail`, `ChannelView`) with channel messages
  immutable by contract. Steer reads `board.lastMessage`, compact
  lands its folded prefix in one batch, and `ExtractTurnMessages`
  reads through `ChannelView`. (#202)
- Stream deltas are coalesced before they reach the window (one
  `stream` event per 25 ms, 64 KiB or 128 deltas, flushed ahead of
  every non-stream event so `turn_end` stays behind the text it ends),
  and SQLite handles open with `synchronous=NORMAL` instead of `FULL`
  — WAL still acknowledges each commit before it is written, and every
  commit pays half the fsyncs. (#187)
- Runtime assembly is attributed in the log (`host: runtime assembled`
  / `invalidated` name the workspace, duration and reason), and
  folding an oversized transcript condenses its shards in parallel (at
  most four in flight), sending one oversized message as pieces
  instead of truncating it. (#189)
- Skills are user-root only: `<workspace>/.agents/skills` is no longer
  scanned, and discovery, `skill_install`, `skill_create`/`modify` and
  the import dialog all address `~/.agents/skills`; the tools' `scope`
  argument and the retired `skills.settings.work_dir` are gone. (#187)
- Users overriding the assistant graph from disk (a `{file: …}`
  `assistant.yaml`) need the graph's new `steer` node too: copy
  `graphs/nodes/steer.js` alongside it, or turns stop starting. (#181)
- Which assembly serves a workspace is a question only the Host pool
  answers, asked per workspace: the desktop's process-wide "current
  Host" pointer, its armed-replacement set, its configured-Host map
  and its background-acquire entry point are gone. Replacements are
  armed on the pool (`ScheduleReplacement`, waiting out the retiring
  Host), the adapter is consulted only for whether a workspace is
  still wanted, and concurrent acquires share one assembly. (#189,
  #213)
- The retry that carries a call across a Host retirement is one
  function (`Runtime.Do`): the window (never past ten seconds), the
  attempt budget and the classification have one home, and the first
  attempt always resolves against the workspace the call named, not
  whatever Host is current. (#213)
- Turns get twice the wall clock: `build.timeout` and
  `policy.run_timeout` both move from 1h to 2h, and the run timeout is
  the one bound a turn can hit. (#211)
- Renderer sampling separates "not a frame" from "a slow frame": the
  sampler pauses while the window is hidden, gaps past a second count
  in `dropped_gaps`, frames are counted in `long_frames_50`/`_100`/
  `_200` with the view named, and a slow interaction is attributed to
  its cause; telemetry comes from the workbench window only, and LCP
  is no longer collected (an always-open SPA keeps raising a running
  largest-paint). (#209)
- The chat pane's top corner is one activity card — the plan, the
  latest thought and the conversation's live sandboxed processes —
  belonging to the conversation: a turn ending does not take it down,
  sections fold themselves, and the process section lists what is
  still running with a bounded tail, fed by `opencraft.processes`.
  Reasoning folds into the store at 250 ms while prose keeps 100 ms.
  (#192, #193, #194)
- The sidebar folds a workspace's session list at four rows instead of
  ten, so collapsed nodes stay on screen. (#201)
- The app paints its own checkboxes, radios, number fields and sliders
  from one skin (`styles/controls.css`) that reads only design tokens,
  so a future theme overrides all of them at one address; number
  fields keep a cleared field a draft instead of committing a zero.
  (#206)
- The transcript is windowed in the DOM, not just in the store: past
  24 blocks the list renders the blocks around the viewport and
  reserves the estimated height of the rest, measurements kept by
  block key so history prepended underneath keeps its heights, and
  jumps aim the list at the block that owns the message first.
  Find-in-page no longer reaches messages that are not mounted — that
  is what windowing costs. (#215)
- Long turns no longer freeze the view, and history is paged:
  `TurnBlock` is memoized, process rows virtualize, large patches fold
  behind one row, the store caps items and reasoning and commits
  streamed text on a flush interval, and `apply_patch` now accepts
  standard unified diffs (`toUnifiedDiff()` copies one out; rename,
  copy and binary hunks are refused with a clear error). Opening a
  conversation reads `INITIAL_HISTORY_TURNS` and pages backwards
  (`Session.Turns(id, limit, beforeSeq)`), capping on turn boundaries.
  (#180)
- Memory is bounded on the paths that grew: a rebuilt Host is
  collectable again (the configure-once marker dies with its Host),
  the skills index stores flat postings (0.93 MB → 0.41 MB with
  identical ranking), `grep` retires oversized files on the walk's own
  stat and reports matching binaries by path, and the session cache
  drops to three minutes idle. (#182)
- Diagnostics answers three questions instead of being one flat
  column: the environment facts, command check, PATH editor, log pane
  and charts are always visible (the last three behind dialogs
  portalled out of the settings panel), a DEV switch gates the
  renderer sampler, heap snapshots, the OTLP sink and the command
  pool, and the sampler is a real start/stop switch
  (`diagnostics.perfProbe`). Every chart declares how its samples fold
  — a running value keeps, an event averages, a worst takes the max, a
  count sums — with its own unit, and TTFB and CLS are gone with the
  instruments that cannot measure them under `wails://`. (#180, #183,
  #185)
- Tool audit logging, the compact fold and log reads got bounds: audit
  rotates and truncates its file, compact shards a large fold at
  400 KiB, and `Settings.ReadLog` tail-reads; Diagnostics gains a
  heap-profile card. (#180)
- Turn artifacts have exactly one source — the workspace write
  observer, buffered by the host and flushed by `ArchiveTurn` — and
  the per-turn manifest snapshot reconciliation is gone: two full tree
  walks per turn whose only reader was the archive. (#198)
- Surfaces, text and charts read one token ladder (new rungs for
  raised rows, meta text, scrims and a series palette, replacing
  translucent fills and raw hex), and Overlay, Popover and Tooltip
  move onto one stack that owns Escape (topmost layer only), focus
  trap and restore, scroll lock and click-outside, with one shared
  `ConfirmDialog`. (#174)
- The sandbox supervisor speaks protobuf over a private per-child
  channel — a `socketpair` passed as fd 3 on Unix, a named pipe on
  Windows, closed as soon as the child takes the descriptor over —
  instead of JSON-RPC on a per-child socket, and the exposure that
  design carried goes with it: a sandboxed command can no longer read
  or forge frames on the host channel. `Hello` compares the protocol
  version before anything else, a request runs on its own goroutine
  with the frame's deadline as a real context, and its `Cancel` is
  registered before the handler starts. Sandboxed children come from a
  pool: it forks nothing until the first sandboxed command and keeps
  `Prewarm` children warm afterwards, its active bound is a reuse
  bound rather than a refusal — a lease beyond it gets a dedicated
  child, reclaimed when its runner closes — a settings change trims
  idle children immediately, and the orphan sweep verifies a child's
  identity (launch nonce, or image name plus creation time) before
  killing anything. Settings ▸ Diagnostics gained a Command pool card
  with the knobs and the live idle/active counts. (#165)
- flowcraft core moves to v0.4.5: a failed MCP liveness probe now
  closes the session it dropped (every reconnect used to leak the
  stdio child tree), a server negotiated at `2026-07-28` is watched
  through its connection instead of pinged — that revision removed
  `ping`, so a healthy server was torn down and redialed every fifteen
  seconds — and a per-server `liveness` can pin the probe or switch it
  off. (#167)
- `apply_patch` renders as a card instead of a bare diff block: a
  summary header with per-file glyphs and totals, a skeleton while the
  patch loads, and the result JSON only when it adds something. The
  viewer wraps long diff lines in chat and sizes rows to content in
  panels, a painted scrim replaces the CSS mask on the scroll box, and
  "Show full diff" says when lines are hidden. (#168)
- The prompt is two board vars, so the provider's cache survives a
  rephrased question: `world.sections` is the cache-stable prefix
  (instructions, environment, permissions, `AGENTS.md`, then the
  folded summary and the raw window), and `world.tail_block` — the
  plan, the ranked skills list, the activated skill bodies — rides on
  the user's own message, the one position expected to differ every
  turn. Compaction decides from a measurement rather than a character
  estimate: the graph records each call's usage, the compact node
  stamps the channel length that call saw, and the pair persists per
  conversation as the next turn's anchor (with no measurement yet, the
  node waits one round). `max_compactions` becomes a
  consecutive-failure backoff plus a per-turn fold budget, and a turn
  still over budget is asked to wrap up once, never between a tool call
  and its result. (#170)
- flowcraft drivers move to v0.3.3: anthropic, openai and bytedance
  report cache-inclusive prompt totals, so a conversation served mostly
  from cache no longer reads as an empty one, and negative or
  contradictory wire counters are clamped instead of shrinking the
  total. The normalization the context work added at the sessions
  boundary degrades to a pass-through as a result, without a code
  change. (#171)

### Fixed

- The file viewer follows a theme flip again: CodeMirror takes its
  colour scheme as a prop, and the pane read the class on
  documentElement once per mount, so a theme switch (or the OS
  flipping under "auto") left the editor on the old scheme. (#216)
- Folding survives a condensation that produces no text: a reasoning
  model can spend the whole output budget thinking, and the fold then
  failed with no fallback, sending an over-window request that killed
  the turn. Compact probes the deployment's compiler, retries once
  with the cap grown past the observed spend, and folds a model-free
  mechanical digest when no summary arrives. (#175)
- The turn ruler stays one dash per turn past 40 turns: it draws the
  turns around the middle of the viewport and slides that block as the
  transcript scrolls (clamped at both ends, where the cut edge fades),
  and the scrubber's hit area follows the block. (#177)
- Opening a conversation from another one lands at its newest message
  again: the stick-to-bottom pin lived in refs that outlive the
  scroller, so the fresh transcript rendered at its top with the
  jump-to-latest pill up. It is re-pinned before paint. (#178)
- The ⌘K palette opens with the caret in its search field, closing a
  dialog hands focus back where it came from, and an approval prompt
  arriving while the caret is in the composer owns the keyboard and
  hands it back when answered. (#205)
- Dialog and menu chrome: a settings dialog no longer opens clipped
  inside its panel (entrance animations fill backwards; tool dialogs
  portal to the body), an open menu no longer closes because a
  scroller elsewhere moved, and the macOS traffic lights stay on the
  chat header. (#203)
- The file tools work on Windows again: `read_file`, `write_file`,
  `list_dir`, `grep` and `glob` judge cleanliness with `path.Clean`
  (the slash form they emit was rejected below the root), `list_dir`'s
  `max_depth` counts `/`, and `apply_patch` judges patch paths in the
  workspace namespace. Both packages run in Windows CI now. (#202)
- Path containment is single-sourced: nine callers use `pathsafe`
  instead of spelling the comparison by hand, and plugin resolution is
  fixed with it — a missing leaf behind a symlinked directory used to
  pass, so "escape/new.txt" was accepted out of a plugin. (#164)
- A process started in a turn's last poll gap is no longer invisible
  until the next message, and a sandboxed session read that stopped at
  its own deadline reads "no output yet" instead of the child's wire
  error.
- One GUI per state root no longer rests on the shell's
  single-instance machinery: the app takes a non-blocking lock on
  `<state root>/gui.lock` and a rejected second launch raises the
  holder's window and exits 1. (#190)
- Automation and tool loose ends: a correction typed during a
  scheduled run reports its undelivered steer count; a cancelled wait
  no longer crashes the turn goroutine; the `automation` tool's
  nested `task`/`schedule` schema reaches the model intact; and a
  scheduled run no longer cuts short a turn the user is watching.
  (#181)
- A staged draft — the Tab queue, or Enter pressed while the previous
  send was still starting — runs in the workspace that owns its
  conversation instead of the one on screen, and a start for a
  conversation no store owns is refused rather than minted somewhere.
  (#181)
- A completion notification that races a store close falls back to the
  app name instead of logging `sql: database is closed`, and a
  settings write that changes nothing no longer invalidates the
  runtime. (#187)
- The delegation card wrote `delegate.policy.settings: {}` when both
  target lists were empty — flowcraft rejects an empty settings
  subtree, so the whole document failed to build and every workspace
  stayed unassemblable until repaired by hand. Empty lists are written
  as `[]`, and the other settings writers omit an empty key. (#207)
- The post-turn review actually runs now: it gated on a tool count
  read from the engine's narrowed `Result.Messages`, which never
  carries a tool result, so every successful turn counted zero tools
  and the review and its memory suggestions never ran. The turn is
  projected from the board now. (#208)
- A turn the deadline ended no longer reads as a turn the user
  stopped: the host's structured `error_kind: "timeout"` decides, the
  transcript says "Reply timed out" with the actions an interrupted
  turn offers, and the native banner stops calling it "Task
  cancelled". (#211)
- A produced file is filed under the turn of the run that wrote it:
  the artifact buffer and turn timing are keyed by `(conversation,
  run)`, the `artifact` event carries `run_id`, and a write whose run
  is not in the transcript yet lands on the trailing live entry.
  (#212)
- Host lifecycle fixes: every call resolves by workspace through the
  pool (a start could be resolved against the wrong workspace's Host),
  a Host handed out for work is wired with the adapter's configurator
  first whichever path handed it out, and a call that waited out the
  retry window no longer answers "runtime is not ready". (#213)
- The transcript is bounded in the renderer instead of growing with
  the archive: a conversation settles to 16 MiB of inline media and
  16 MiB of text on every write path — the newest 24 messages
  untouched, the oldest giving up bytes first — and one tool result is
  capped at 256 KiB as it lands. (#214)
- Long unbroken markdown tokens wrap instead of forcing the transcript
  into horizontal scroll, and plugin panels are wrapped in
  `ErrorBoundary` scopes so one crashing panel no longer takes the
  page down. (#180)
- A text block the model has finished writing no longer sits on screen
  in raw markdown until its turn ends (the "still streaming" flag now
  marks only the trailing block of a running turn), and an `ask_user`
  card's header no longer loses its question to a long answer. (#200)
- An expanded session list resets when its workspace collapses, and
  react-markdown's `node` prop is stripped before props are spread
  onto real elements — React 19 was writing `node="[object Object]"`
  into the DOM for tables and code fences. (#176)
- A tool call with a bad argument names it and lists the accepted
  ones: `toolargs.Decode` rejects unknown keys by name and lets a tool
  declare aliases, so `exec_command` accepts `cmd` for `command` — the
  slip models kept making, 26 of 324 exec calls in one session coming
  back as "command is required" and costing a round trip each. A failed
  `apply_patch` hunk names itself and why it did not match, `view_image`
  reports a missing file as a missing file instead of "workspace: not
  found", and `grep` says the path must be a directory. (#160)
- A file is classified by its leading bytes, not its name: `.ts` is
  TypeScript, but the platform's table called it `video/mp2t` and the
  preview rendered a `<video>`, and nothing told a screenshot saved as
  `.txt` from a note. One classifier reads the sample (net/http's
  mimesniff), lets the name refine what the bytes cannot say, and falls
  back to the table; a payload without a NUL byte is text, whatever the
  name claims. The preview, attachments, the media stream handler and
  `read_file`'s image hint all read it, and the hand-maintained
  code-extension whitelist is gone. (#163)
- An oversized JSON tool result stays parseable: truncation runs after
  the result limit and excerpts inside the string fields before
  re-encoding, instead of cutting the envelope and handing the model
  something it cannot read. (#165)
- The log keeps the signal and drops the noise: a subagent context,
  which is not a persisted session by design, no longer warns on every
  skill activation, quitting detaches the notification sink before the
  runtime closes instead of logging `sql: database is closed`,
  forwarded plugin stderr masks credential query values, a git failure
  names its repository and its argv, an installed OTLP sink is a
  configuration event rather than a warning, and a forwarded renderer
  error keeps its message and stack on one physical line. (#166)
- Workspace history shows the order it just recorded: the open is
  written before the `ready` event that makes the UI re-read it, equal
  or unparsable stamps rank stably by title and path, and the frontend
  drops a response overtaken by a newer request and keeps an identical
  snapshot, with the active workspace promoted in render only. (#168)
- Escape is owned from the moment an overlay renders: the handlers move
  from a passive effect to a layout effect, closing the task-wide
  window in which Escape went to the surface underneath — the defect
  that left a just-opened preview dialog open. (#168)
- A stopped turn is archived: the archive hook wrote with the run's own
  context, already canceled on a Stop, so the first store call failed,
  the error was swallowed, and the turn, its tool activity and the
  user's message left no trace for the next replay. Archive writes use
  a context without cancellation now, and a write that fails says so.
  (#169)
- Compaction folds instead of stacking summaries: the compact node
  wrote its summary and left the folded prefix on `MainChannel`, so
  every over-budget turn re-sent the whole history and spent its
  condensation budget without shrinking anything. The prefix moves to
  the archive channel before it is removed, the turn's user message and
  a tool call's pairing are protected at the boundary, a failed fold
  changes nothing and remembers where it stopped, and memory re-unions
  the side channel so nothing leaves the archive for being folded.
  (#169)
- Cache statistics are honest across providers: Anthropic's wire
  `input_tokens` counts only the tokens neither read from nor written
  to the cache, so a hit rate could exceed 100% and the UI clamped it
  to a confident "100%". Prompt totals normalize to the inclusive
  reading at the sessions boundary, `lib/usageRate.ts` is the one
  formula behind the hero and the settings page, a row that cannot
  state a ratio shows a dash, and the cost notices land where the
  change is made — a model switched mid-conversation, a provider
  credential changed, a turn that folded. (#170)
- The compactor's patch parsing never worked: a tool result's content
  crosses the board bridge as a parts array, not a JSON string, so
  `JSON.parse` threw every time — folding had never applied, and every
  over-budget turn paid for a summarization call and re-sent the full
  history. `world.compact.epoch`, a per-turn fold count read as a
  cross-turn generation, splits into `epoch_total` (persisted with the
  anchor) and `folds_turn` for the UI. (#170)
- Quitting no longer drops the last thing a crashing plugin did: the
  capability manager tracks its exit watchers and `Shutdown` waits on
  them, so an audit line written by the crash handler lands before the
  data directory can go away, and a manager that has shut down refuses
  to start a process whose lifetime nothing would own. (#173)

## [0.5.3] - 2026-09-17

### Added

- `web_search` gives a deployment a search path that does not depend on
  the provider hosting one: auto mode spreads conversations across the
  keyless Parallel and Exa hosted MCP endpoints and fails over to the
  other once, while Tavily and Brave call with a key the user supplies
  (stored as `websearch/<provider>`; the document keeps only the
  secret reference). The tool returns links plus a bounded excerpt
  block — `web_fetch` still owns reading pages in full — and Exa's
  free-tier notice, which arrives as an ordinary 200 text payload, is
  mapped back to a rate-limit error so it cannot masquerade as "no
  results". Responses are capped at 1 MiB with a 15s default timeout
  and at most 10 results, the model may only set query, count,
  freshness and domains, and the host always owns the endpoint.
  Settings > Tools gains the matching card: provider, key, result
  count, timeout and endpoint override under advanced, and a real test
  query. An override must be https (plain http only on loopback) and
  rejects query, fragment, userinfo and redirects. (#157)
- Settings > Tools is one page for the generation tools and the MCP
  servers: an item opens the same centered dialog MCP details use,
  listing every enabled deployment that serves that output with the
  provider-specific knobs its driver declares — the app's own enum,
  number and unset/on/off controls, each next to the default the
  driver documents and a one-line explanation. Presets stage the
  common requests (no watermark, 2K, transparent background, with
  audio, fixed camera) and nothing is stored until the user saves,
  because an unset knob is the state that keeps the provider in charge
  of its own default. Values persist in the user layer under
  `resources.tool.imagegen` / `resources.tool.videogen` keyed by
  deployment id, and the vocabulary, validation and pruning live in
  `foundation/config`: an unknown provider, an out-of-range or unknown
  enum value and a knob block whose provider was removed all fail
  before anything is written. (#152, #154, #155, #156)
- A command the OS sandbox refuses is no longer a dead end:
  `exec_command` recognises a confinement refusal in the finished
  command's output (EPERM/EROFS/access-denied phrasings, including
  PowerShell's .NET wording; a plain "permission denied" deliberately
  does not match), asks whether it may run outside the sandbox once,
  and re-runs the exact same argv with the full environment, marking
  the result with a `note`. "Always" remembers the rule in the
  workspace's `escalations.yaml` — deliberately its own file, because
  a downgraded build rewrites `approvals.yaml` from a narrower struct
  — and every decision (once / always / deny) is appended to
  `<workspace>/audit/escalations.jsonl`. Only workspace sessions
  escalate (read-only keeps its guarantee, YOLO has no confine), only
  `exec_command` is prompted, remembered rules cover one-shot commands
  but not TTY sessions, and a host with no user to ask keeps the
  original failure. Settings > Permissions lists and revokes the
  rules. (#146)
- The process PATH is resolved once at startup and used by every
  spawn: `foundation/utils/envpath` appends the user's prepend list,
  the inherited PATH and the platform's install directories (Homebrew
  on macOS; `/usr/local/bin`, snap and Linuxbrew on Linux; plus the
  per-user bin directories), removes nothing but empty entries and is
  idempotent, so a Dock-launched app can find Homebrew, npm-global and
  user-local binaries for MCP stdio servers, agent commands, `gh` and
  `git` alike. Settings > Diagnostics gains a Process PATH card that
  groups the entries by origin, flags rejected and missing
  directories and edits `desktop.json`'s `path.prepend`; saving
  re-resolves and reloads the runtime so MCP servers reconnect with
  the new environment. (#147, #148)
- Generated images and videos play inside the app. The desktop serves
  workspace media over a loopback endpoint (127.0.0.1 on a random
  port, a per-launch random token, GET/HEAD only, traversal and
  symlink escapes rejected, byte-range seeking), the viewer plays
  videos inline and falls back to the system player, and generation
  cards render thumbnails, inline players and a streamed-preview
  section. The image tool gained count, seed, quality, reference
  images, mask and partial-image previews, and the video tool gained
  aspect ratio, seed, bookend frames and reference videos/audios, with
  the input roles validated instead of left to the provider. (#151)

### Changed

- flowcraft core moves to v0.4.4 and the drivers to v0.3.2, carrying
  the upstream fixes this repository reported: generate fallback skips
  a target whose declared outputs cannot serve the request, and
  `tool_search` reports per-round visibility instead of pool
  membership, so a name that loses the round's budget answers
  `visible_budget`, an oversized definition no longer stops the byte
  walk, and the best-ranked hit of a batch wins the tie. Provider
  knobs left the model-facing schemas with this work: `generate_video`
  no longer offers thirteen Seedance/MiniMax knobs, `generate_image`
  keeps only its per-call inputs, and both decode arguments strictly,
  so a knob the schema does not offer fails loudly. The per-round tool
  budget rises to 64 definitions / 48 KiB with a 48-tool / 32 KiB
  discovery pool, measured against the embedded catalog, and tool
  results list the fields a driver dropped instead of dropping them
  silently. (#152, #153, #156)
- Hosted web search is drift-proof: `StartRun` filters the board
  extension bag against the live runtime generation's decoders, so an
  entry naming a deployment the serving generation does not have is
  dropped with one warning instead of failing the turn at the
  inference node. Eligibility resolves the driver impl rather than the
  catalog provider id, which restores the checkbox for custom rows
  and rows with an explicit driver, `deepseek/deepseek-v4-pro` is
  marked search-capable, and the checkbox explains which upstreams
  honour the tool. (#149)
- Windows gets a real shell: `foundation/utils/shelldetect` resolves
  PowerShell 7 → Windows PowerShell 5.1 → `cmd.exe`, skipping the
  Store execution-alias stubs a restricted token cannot follow, and
  the exec tool, the description the model reads, execd's environment
  report and the diagnostics page all read it instead of the
  hard-coded `/bin/sh -c` that made shell-syntax commands fail there.
  Windows bare words accept `\`, so `C:\tools\x.exe a b` stays on the
  direct-argv path. (#146)

### Fixed

- Settings > Diagnostics no longer blanks the page on a healthy
  machine: the PATH DTO serialized its empty list fields as `null` and
  the card read them as arrays, which fired on any machine without a
  rejected or missing directory. The lists are normalized on the wire
  and read defensively, the candidate table is split per platform so
  macOS no longer reports `/snap/bin` as "not installed", and a
  prepend directory that does not exist yet is flagged in place
  instead of disappearing. (#148)
- The pet is pulled back onto a display after a monitor is unplugged
  or the layout changes: the placement check clamps a stranded
  position onto the closest work area rather than the primary
  display's corner, keeps adopting an OS-reported position that is
  still on a display, skips a drag in progress, and measures the drawn
  character rather than the transparent stage around it. (#150)
- Generation-only models no longer sit in the composer and automation
  model pickers, where a chat turn could never route to them: the
  option list keeps only models that can serve a text-output chat
  request, which also lets the Auto reasoning hint resolve against a
  real text target. (#151)
- A stdio MCP server whose command cannot be spawned is pre-flighted
  (PATH for bare names, the child PATH for `#!/usr/bin/env`
  interpreters) and reported with the concrete reason in both the
  status list and the test button, instead of retrying forever behind
  a "connecting" status. (#146)

## [0.5.2] - 2026-09-16

### Fixed

- `execd` treats a client that already hung up as teardown instead of
  warning about the failed write, so a session watcher firing after a
  client disconnects no longer leaves a spurious record in the app log
  (or, in tests, in the next test's capture). (#143)

## [0.5.1] - 2026-09-16

### Added

- The agent can author and install plugins without leaving the
  conversation: `plugin_install`, `plugin_update` and `plugin_list`
  copy a plugin from a workspace directory or `.zip` package into the
  same registry Settings > Plugins uses and reload the runtime
  afterwards, so a plugin written in the workspace is live from the
  next turn on. Every mutation asks the user to confirm first, and the
  dialog shows the id, version, permissions, entry bundle and
  capability binary before anything is copied. Sources keep the file
  tools' workspace confinement (absolute paths, traversal and symlink
  escapes are rejected outside YOLO sessions), while manifest
  validation, version ordering, builtin shadowing and zip-slip checks
  stay with the registry. A runtime with no interactive user —
  automation runs, headless — fails closed and exposes none of the
  three tools, and the new built-in `plugin-creator` skill documents
  the manifest, bundle and capability-subprocess contracts. (#139)

### Changed

- `google.golang.org/grpc` moves to v1.83.1 with the OpenTelemetry
  modules at 1.44.0, clearing GO-2026-6348 — heap exhaustion through
  HTTP/2 DATA frame fragmentation — which the vulnerability database
  published against the 0.5.0 pin after that release. (#137)

### Fixed

- A stopped `execd` child no longer loses the stderr lines it writes
  while shutting down: the forwarder ran detached and `exec.Cmd.Wait`
  closed the pipe's parent end the moment the child was reaped, so
  shutdown lines could be dropped, or land after stop returned — in
  the next session's log window and, in CI, the next test's capture.
  The pipe is explicit now, the forwarder drains to EOF, and stop
  waits for it under the same bounded teardown grace period — which is
  only reachable while a grandchild holds the write end, because the
  parent releases its own copy when the child starts. Without that
  release the read end never saw EOF, so stop always fell through to
  the grace period, dropped the child's last lines, leaked a
  descriptor per child and could still emit a line after returning.
  (#138, #141)

## [0.5.0] - 2026-09-16

### Added

- Settings > Interface gains appearance settings: the interface and
  code fonts are chosen from the families the host enumerates
  (CoreText on macOS, GDI on Windows, fontconfig on Linux) with a
  searchable picker, and a text-size scale applies through CSS
  variables. The choice lives in the desktop preference document with
  a localStorage mirror, so the first frame paints before the binding
  resolves. (#130)
- Built-in model template catalog: `foundation/config` embeds
  `inference_templates.json` (read as `Config.InferenceCatalog`)
  whose entries reuse `InstanceSpec`/`ModelSpec`, decode strictly,
  record their source and are validated through the same path a
  settings save takes. Settings shows templates as one-click pills
  next to the driver picker plus a searchable model list per row;
  picking a model only writes model fields, and a declaration that
  needs a provider-level fact (video input) warns instead of editing
  the provider behind the user's back. (#127)
- Capability plugins that declare `telemetry:export` can point the
  app's OTLP export (logs, traces, metrics) at their own collector
  through `telemetry.configure` / `telemetry.disable`. Collector
  credentials stay in memory, one sink is active at a time,
  `OTEL_EXPORTER_OTLP_ENDPOINT` keeps priority, and the sink is
  dropped when the plugin is disabled, uninstalled or crashes. The
  switch sits in Settings > Diagnostics next to the log viewer, and
  install/remove/deny changes land in
  `~/.opencraft/audit/telemetry.jsonl`. (#134)
- `view_image` returns workspace images to the model, oversized prompt
  images are downscaled, and models that declare video input get the
  bytes passed through, all on flowcraft's multimodal tool results.
  (#119)
- The pet reacts to hover — a wave on the first hover of a session, a
  glance afterwards — and walks while it is dragged, turning to face
  the direction of travel. (#131, #132)

### Changed

- flowcraft core upgraded to v0.4.1 with drivers v0.3.0: the
  per-vendor drivers collapse into one per wire family, tool results
  carry multimodal content, and the inference layer no longer ships a
  built-in model catalog or vendor presets — drivers are declared as
  deployment data and the user document is rendered from a pinned
  template. (#119)
- Turns are bounded by a run timeout instead of a node budget:
  `build.max_iterations: 0` lifts the graph loop guard and the agent
  policy's `run_timeout` (1h) ends the run, and a turn that ends on a
  deadline carries `error_kind: timeout` and renders a localized
  notice. (#133)
- Inference configuration has one row contract: `InstanceSpec` /
  `ModelSpec` are the single wire shape shared by the settings page
  and `inference.upsert`, `Lower` is the only way in, and the write
  rules live in `foundation/config`. The opaque `provider_spec` bag is
  gone (every provider-spec leaf the drivers accept is typed,
  including `reasoning_scope`), a plugin-declared vendor row no longer
  blocks settings saves, a managed row's `enabled` toggle belongs to
  the user and survives a plugin re-upsert, and plugin profiles are
  decoded strictly — `id` → `stable_id`, `provider_spec` →
  `advanced`, `enabled` no longer plugin-settable, with no
  compatibility shim. (#123)
- The inference advanced panel is grouped by driver section and uses
  the page's own dropdowns, and the model input-token field is wider
  with a hint that no longer claims a driver default that does not
  exist. (#124)
- Path containment is single-sourced: `foundation/utils/pathsafe`
  (`Within`/`Rel`/`ResolveUnder`/`RelRef`/`RealWithin`/`RealDir`) now
  backs the file/git bindings, skill installs, media attachments and
  plugin manifest/zip-slip checks that previously hand-wrote the
  comparison in fourteen places. (#120)
- Both JS graph mirrors are gated by fixtures generated from Go:
  `compact.js` against `summarytext` (including the summary prefix)
  and `world.js` against `worldstate`, with the real embedded scripts
  run by `frontend/src/lib/compactMirror.test.ts` and
  `worldNodeMirror.test.ts`. Writing the compaction gate fixed two
  real bugs: structured `tool_result` content counted as
  `[object Object]`, and tool-call arguments were estimated from a
  spelling Go does not render. (#120)
- The desktop pet no longer roams: it stands still while idle and only
  walks to its watch spot while the agent works or asks. The window
  anchors on the character's drawn pixels instead of the window
  rectangle, the stage shrank to 168px with hit-testing that only
  starts an interaction on visible pixels, and the disposition is
  renamed to idle (Settings > Diagnostics shows "Idle"). (#128, #131,
  #135)
- Automation notifications no longer ride the UI event bus:
  `Shell.Notify` hands a payload straight to the notification sink
  without requiring an attached window, notification kinds are named
  constants, and the banner copy is assembled by a unit-tested shaper.
  (#121)
- Child diagnostics reach the app log: the execd child installs a
  stderr-only sink forwarded line by line with `execd.pid` /
  `execd.socket` tags, capability-plugin stderr is forwarded as WARN
  records with `plugin.id` / `plugin.line`, normal teardown is no
  longer logged as a warning, and sockets older than a day are swept
  once per process. (#135)
- A plugin write that renders an unchanged inference document no
  longer rebuilds the runtime, and the assistant graph declares an
  explicit default branch for its two splits instead of relying on
  their routing variables being booleans. (#135)
- Formatting is checked with the toolchain `go.mod` pins instead of
  whatever `gofmt` is on PATH, imports are grouped by goimports
  through golangci-lint, and the last dead UI compatibility branches
  (the legacy turn marker strip and `artifact_sync`) were removed
  after confirming that no build ever persisted them. (#122)

### Fixed

- Conversations containing a tool call from before flowcraft core
  v0.4.0 can be resumed again: workspace migration 015 rewrites the
  string content of tool results in `archive_messages` and
  `memory_items` into canonical parts, and the desktop transcript
  reads those parts for archived and live results alike. (#125)
- `view_image` output is no longer dropped by the result budget: the
  raw cap dropped to 786000 bytes so base64 expansion plus the JSON
  envelope fits in 1 MiB, and oversized files are rejected through
  `workspace.LimitedReader` instead of being read whole. (#126)
- `apply_patch` accepts `input` as an alias for `patch` and rejects
  unknown keys by name, so models that emit the codex-harness shape no
  longer produce an empty patch; the tool card only asks the backend
  to render a patch when the arguments carry one, and its copy button
  is no longer nested inside the clickable row toggle. (#129, #135)
- The usage trend defaults to all models: an empty model aggregates
  the series across every model, and the picker offers an "All models"
  entry instead of preselecting the lifetime top model — a name that
  may no longer run, which left the chart empty while the table was
  full. A range without usage now says so. (#129)
- Turn errors are classified from types instead of the rendered prose:
  `host.ClassifyRunError` reads `interrupt_cause` from
  `agent.InterruptedError.Cause` and `error_kind` from
  `inference.Error.Kind` (plus a harness-level timeout), migration 014
  adds the columns and backfills existing rows with rules more
  tolerant than the renderer they replace, and the frontend only reads
  fields — the text parsing and its generic fallbacks are gone. (#121)
- Pet hover used to fire only when OpenCraft happened to be frontmost,
  because a webview sees mouse moves only while it is the key window:
  hover is now polled from the global pointer (NSEvent on macOS,
  GetCursorPos on Windows), the hit box is the drawn box plus a 4px
  pad, hover also fires while the pet is working or asking, and
  Diagnostics shows the polled pointer state. Linux has no
  global-pointer source yet, so hover stays inactive there. (#132)
- A capability plugin that writes a response larger than 64 KiB no
  longer hangs the host: the scanner got an explicit 8 MiB cap, a
  failed read logs the plugin id and stops the process, and a cached
  process whose done channel is already closed is not handed out.
  (#135)
- The pet reads the runtime's view model count instead of probing one
  index past the end, which logged a console error on every mount.
  (#135)

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
