# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Past sessions are searchable by the assistant, not only by the UI: the
  `session_search` tool runs a full-text recall over this workspace's
  archived turns (SQLite FTS5, trigram tokenizer, so a substring inside
  Chinese text matches too). The index stores the message's prompt
  projection — its text plus the tool lines a reader would see, never the
  stored JSON — is written in the same transaction as the archive row,
  and databases written before it existed are backfilled once at open.
  Queries shorter than three characters fall back to a substring scan
  with the fallback reported in the tool's note, hits are de-duplicated
  per conversation, and each hit carries a snippet with the match marked.
  The tool is deferred: the model discovers it through tool_search.
- The assistant can remember facts across sessions. A user-level store
  (`user.db`) holds them with the workspace and conversation they came
  from; the `remember` tool asks for confirmation before writing,
  applies the same secret rules the tool-result middleware applies and
  refuses text that looks like a credential instead of storing a
  redacted version. Dedupe, caps and provenance live in the store, so
  the settings page, the tool and an accepted review suggestion all
  write through one path. Live facts are injected at the top of every
  turn (bounded by count and by a byte budget, newest first, with the
  number that did not fit reported) and the section is re-read each
  turn, so an accepted fact is live on the next one. Settings ▸ Memory
  gained a card for browsing, editing, staling and deleting facts.
- A post-turn write-back review proposes facts worth keeping. When
  enabled — it ships off — and the turn meets the configured cadence and
  tool-use threshold, one inference call reviews the finished turn and
  returns strict JSON candidates; they wait in a pending queue in the
  settings card, and accepting one writes it through the same user-memory
  path as the tool. A review never reviews its own work (the recursion
  guard covers the marker, the conversation and the in-flight slot),
  never spends calls on subagent traffic, and records its own usage.
- Skills track how they are used and can be retired without losing
  anything. Each activation records a use under the skill's key; the
  skills page shows uses, last used, pins and retirement state, and the
  curator marks idle rarely-used skills as candidates. Retiring a skill
  snapshots it to a tar.gz under the app home and records the archive;
  restoring puts it back. Builtin skills are never candidates and no
  file is ever deleted.
- Delegated runs are visible end to end. The desktop now supplies the
  stream resolver and exporter core's delegation service takes, so an
  asynchronous delegated run's deltas land in the conversation that
  asked for it and the finished run's result flows back into the parent
  transcript once (idempotent by child run). The SubagentDock nests a
  delegated run under the run that spawned it (collapse/expand, open the
  child conversation), and Settings ▸ Tools gained a delegation card:
  the concurrency and depth limits plus the curated allow/block target
  lists, enforced on every delegate call — hiding a target from the
  listing is a hint, a call that names a restricted target is refused.
- Mid-turn steering: a message submitted while a turn is running now
  reaches the model at the next tool-round boundary instead of waiting
  for the turn to end. Enter in the composer sends it
  (`Conversation.Steer`); the model reads it as a standalone user
  message right after the round's tool results, and several corrections
  waiting at the same boundary arrive as one message. A boundary
  injects at most one core-sized steer (32 KiB of text); a submit that
  would exceed that is refused with the text left in the caller's
  hands. The transcript keeps the interjection where it was typed,
  written as a note the reply continues under rather than as a second
  bubble opening a turn of its own, and the note carries its own state:
  waiting for a boundary, taken by one, or undelivered because the turn
  ended first. A boundary reports what it took as it takes it, so a row
  that made it says so mid-turn instead of holding "waiting for the next
  step" until the turn ends; what never made it stays in place with
  resend, copy and discard instead of disappearing with the conversation
  archive. A resumed session rebuilds these rows from the archive (a
  boundary's batch comes back as one row, which is all the archive still
  proves), and a turn result whose undelivered count cannot be read
  keeps every such row rather than assuming delivery. (#181)
- Unattended automations get a per-task run limit: `timeout` bounds one
  run (whole minutes, empty = the 15-minute default, at most 24h). A run
  that outlives it is cancelled, its record says `timeout` with the
  bound, and the concurrency slot comes back once the turn settles. The
  editor gains a run-limit field and the run list a timeout badge.
  (#181)
- A live scheduled run can be stopped from the automations panel:
  `Automation.CancelRun` cancels the run context the manager owns, the
  desktop runner's bounded wait turns that into the host's turn cancel,
  and the record is written as `canceled` with no error text once the
  turn settles (archive, memory commit, usage and the concurrency slot
  all still run). The run list offers the action on a running row and
  the task menu on a task whose run is live; the failure notification
  policy stays quiet for a stop the user asked for. (#186)
- A reply the process never finished is no longer lost: every turn now
  writes one checkpoint per completed wave, and the next assembly turns
  a checkpoint with no archive row into an interrupted turn — same
  conversation, same run id, the original user message (media left in
  its URL form) plus whatever assistant and tool output the crash had
  already produced, archived and folded into memory in one transaction.
  The transcript shows it as `interrupted` with the cause `app_restart`,
  and the newest such turn offers **Continue** (say it again as a fresh
  turn, with the partial reply in context) and **Edit & resend** (put
  the message back in the composer). The frontier is deliberately not
  replayed: the wave that was in flight may have run side-effecting
  tools, so there is no safe place to resume from. (#188)
- Settings ▸ Diagnostics grew a Crash recovery card: what the last pass
  did (recovered / already archived / discarded / skipped-as-live /
  failed / not examined) and what the active workspace's checkpoint
  table holds right now. A checkpoint is dropped as soon as its turn is
  archived, so rows left behind are a live run or a turn the next pass
  has to reconstruct. (#188)
- Settings ▸ Diagnostics grew a Provider round-trip probe card (behind
  DEV tools). It wraps the process HTTP transport, so every provider
  call leaves two records — when the request left the process and when
  the response headers arrived — which is the split a per-turn latency
  number cannot show: the gap between the two is network plus provider
  time to first byte, the gap after the previous step is local assembly.
  The switch is `desktop.json`'s `diagnostics.httpProbe`; turning it on
  reloads the runtime, because the provider drivers read the transport
  when they build their clients, while turning it off takes effect in
  place. It parks itself while an HTTP MCP server is configured (that
  client cannot run under a wrapped transport) and says so on the card,
  and `OPENCRAFT_HTTP_PROBE` still forces it on over the switch.
- A second instance can run next to the installed app: `--profile <name>`
  (`OPENCRAFT_PROFILE`) keeps the shared *app home* — `config/`,
  `keyring/`, `plugins/`, `agents/`, user skills — and moves the *state
  root* to `~/.opencraft-<name>`, so a dev copy inherits the user's
  configuration, credentials and plugins while its sessions, `user.db`,
  logs and caches stay its own. `--data-dir DIR` moves both roots (the
  self-contained instance CI and e2e want), `--app-home DIR` moves only
  the content root, and `--config-dir DIR` alone keeps the historical
  `opencraft run --config` rule: the state root becomes the config
  directory's parent. The embedded deploy document resolves content
  through a new `${ocraft:APP_HOME}` value (`${ocraft:DATA_DIR}` keeps
  only state semantics; a contract test pins both halves), the execd
  child resolves no user directory of its own — the sandbox cache root
  travels with the bind request — and every root fallback that used to
  happen silently now logs. The single-instance id is derived from the
  canonical state root (`com.GizClaw.opencraft.d<hex12>`) instead of the
  product name, so one state root keeps exactly one GUI process while
  different roots coexist; a rejected second instance exits 1 instead of
  silently exiting 0, `OPENCRAFT_NO_SINGLE_INSTANCE=1` deliberately
  allows two GUIs on one root, and `desktop.New` runs *after*
  `application.New` so the loser of that race never seeds a directory or
  a log line on its way out. The profile shows up where a user sees it:
  the window title, the tray tooltip and a Settings ▸ Diagnostics card
  naming the profile, the state root and the app home. (#190)

### Changed

- flowcraft core moves to v0.4.8, closing an interrupt that could
  silently do nothing: a sandbox session is spawned with the signal mask
  cleared on a pinned thread and restored afterwards, so a child no
  longer inherits a blocked SIGINT and `Session.Signal(Interrupt)` ends
  the process instead of leaving the signal pending forever (that was
  the intermittent `wait after signal: deadline exceeded` the execd
  suite hit). Walk and Glob hand back slash-separated workspace paths on
  every platform, which is the form this repository's tools and desktop
  bindings already emit; `list_dir`'s depth arithmetic follows it now
  (it counted the host separator, which on Windows would have read every
  path as depth 0 and let `max_depth` walk to the leaf).
- flowcraft core moves to v0.4.7, carrying the narrow board channel
  reads this repository's hot paths asked for: Go gains
  `Board.ChannelLen`/`LastMessage`/`ChannelTail`/`ChannelView` and a
  batch `AppendChannelMessages`, the script surface gains
  `board.channelLen`/`lastMessage`/`channelTail`, `board.appendChannel`
  takes one message or an array of them validated as a whole, and
  "a message on a channel is immutable" is now the Board contract
  rather than a note on `ChannelView`. The consumer side migrated with
  it: the steer node reads its tail through `board.lastMessage`
  instead of projecting the whole channel on every tool round, the
  compact node lands its folded prefix on the side channel in one
  batched append (it never reads the archive back) and stamps its
  anchor from the array it just wrote, and
  `memory.ExtractTurnMessages` reads through `ChannelView` — the
  persistence paths clone what they keep, so the defensive copies were
  pure overhead. The media prepare hook keeps `Channel()`: it edits
  the message it reads and writes the channel back, which the new
  contract requires a copy for.
- Checkpoints are a crash-recovery log rather than a resume cache: a
  session now starts persistent so the engine stamps one per completed
  wave, the host deletes a run's checkpoint once its turn has an
  archive row (a turn whose archive write failed keeps it), and deleting
  a conversation deletes the checkpoints that would otherwise resurrect
  it on the next assembly. `runtime.sessions.resume` stays `false`:
  board seeding, parking and `Resume` are a separate decision, and the
  recovery pass reads only the checkpoint rows. (#188)
- Stream deltas are coalesced before they reach the window: the desktop
  shell folds adjacent text/reasoning deltas of one stream into a
  single `stream` event per 25 ms window (or per 64 KiB / 128-delta
  cap), by the same rule the frontend's own coalescer uses — so an
  already-merged stream passes through it unchanged — and every
  non-stream event flushes the buffer first, which keeps `turn_end`
  behind the text it ends. A token burst that cost one IPC event per
  token now costs one per window; the transcript renders the same.
- SQLite handles open with `synchronous=NORMAL` instead of the default
  `FULL`. WAL + NORMAL still writes each commit to the WAL before it is
  acknowledged, so a crash cannot corrupt the database — the exposure
  is the last transactions not yet checkpointed when the machine loses
  power — and it halves the fsyncs every commit pays, including the
  turn-end archive+memory transaction.
- Runtime assembly is attributed in the log: `host: runtime assembled`
  and `host: runtime invalidated` name the reason (`workspace_open`,
  `settings_save`, `plugin_change`, `inference_change`, …), the
  workspace, the duration, whether a turn was running, and the
  workspace's in-process assembly sequence; an in-place document reload
  logs `host: document reloaded in place` with its generation, and a
  save that falls back to a full rebuild now reports why the swap was
  refused instead of dropping the error. The stream coalescer reports
  its merge ratio on a one-minute heartbeat.
- Skills are user-root only: `<workspace>/.agents/skills` is no longer
  scanned (it was the one discovery input that varied per workspace, and
  the one that put a workspace's own tree on the sandbox read
  whitelist). Discovery, `skill_install`, `skill_create`/`skill_modify`
  and the Settings import dialog all address the user root
  (`~/.agents/skills`) now — the tools' `scope` argument, the dialog's
  scope picker and `RenderSkillPatch`'s scope parameter are removed, and
  a call that still passes `"repo"` fails as an unknown field. A leftover
  `<workspace>/.agents/skills` tree is ignored too — no compatibility
  path, no warning. `skills.settings.work_dir` is retired with it (a
  hand-edited user layer that still sets it fails the strict settings
  decode until the key is removed).
- Enter in the composer now steers the running turn instead of
  interrupting it; Tab keeps the after-turn queue, a draft carrying
  attachments is refused with a hint rather than changed silently, and
  barge-in moves to Cmd/Ctrl+Enter and to a stop button that stays
  reachable while a draft is being written. (#181)
- Automations saved without a run limit are bounded by the 15-minute
  default. Tasks stored before the limit existed are not: the migration
  seeds them with the one-hour bound they used to run under (the
  assistant graph's run timeout), so an upgrade shortens no run nobody
  asked to shorten — the 15-minute default is what a task saved without
  a limit gets, not what existing tasks are given. (#181)
- Users overriding the assistant graph from disk (a `{file: …}`
  `assistant.yaml`) need the graph's new `steer` node too: copy
  `graphs/nodes/steer.js` alongside it, or the graph references an
  asset the deployment does not have and turns stop starting. Graphs
  left at the shipped defaults pick it up on their own. (#181)
- Runtime assembly is no longer repeated back to back. At most one
  replacement is armed per workspace — a second invalidation while one
  is already armed needs no watcher of its own, since that replacement
  assembles from the document on disk when it runs — and concurrent
  acquires of the same workspace share a single assembly instead of
  racing, reusing its error as well as its result. A storm used to build
  one runtime per waker and close all but the first; the `host: runtime
  assembled` count for one workspace is the number that shows it.
- Folding an oversized transcript into memory condenses its shards in
  parallel (at most four requests in flight) rather than one after
  another, and a single message larger than one request is sent as
  pieces instead of being truncated at the cap — the text that used to
  be dropped on the way into the summary now reaches it. The fold logs
  its duration and request count (`compact: fold condensed`), because it
  runs inside the turn and the next request has to carry its summary.
- The graph analyzer's build findings are logged once per process: the
  findings are static and the assistant graph reports the same five
  host-seeded board references on every assembly, so a session with a
  few dozen assemblies wrote hundreds of identical WARN lines into the
  log file and buried everything else. The first occurrence of each
  finding is kept, so a finding that is genuinely new still logs.
- The renderer performance sampler pauses while the window is hidden. A
  background window has its animation frames throttled or stopped by the
  engine, so the gap between two samples measured the throttle rather
  than the renderer — the multi-second `frame_max` samples in the log
  were all background windows. Hiding drops the baseline and resuming
  starts a new one, so `frame_max` only describes visible rendering.
- Crash recovery defers to a live sibling process instead of guessing
  from timestamps: the first assembly of a workspace takes a
  non-blocking advisory lock (`<state root>/workspaces/<id>/live.lock` —
  flock on Unix, a `LockFileEx` range on Windows, dropped by the kernel
  when the holder dies) and a process that finds another one holding it
  runs no pass at all. Every checkpoint it can see is either that
  process's live work or a leftover it deliberately left, so they wait
  for the next start that owns the workspace; the lock file names the
  holder, which is what the diagnostics card and the assembly log
  report. This closes the cross-process case the start-time heuristic
  could not answer, and it matters because `opencraft run` shares the
  state root of a running desktop app by design. A filesystem where the
  lock cannot be taken at all fails open with a warning: a broken lock
  must not silently disable recovery. (#190)
- `engine.BuildRuntime` refuses to assemble without a workspace layout.
  The fallback it used to take resolved the global user data directory,
  which silently assembled a workspace against whatever state root the
  process happened to have instead of the one it was launched with.
  (#190)
- The top-left corner of the chat pane is one activity card instead of a
  plan panel: the current plan, the model's latest thought, and what the
  conversation's sandboxed processes print. The card belongs to the
  conversation rather than to the work: a turn ending does not take it
  down, so the plan the model finished and the last thought stay where
  they were, and the one state it paints nothing in is having nothing to
  report at all — no plan, no thought, no running process — which is why
  a fresh conversation starts without it. There is no close button
  either: the card is the conversation's read-out and it goes when the
  conversation does. Inside that life each section folds itself when its
  content is done — a completed plan, where a later plan call is a new
  checklist that opens at its default — while the thought and the
  process output open on first sight and stay as the reader leaves them,
  because they are themselves what the reader came to watch, and no
  update re-opens one the reader folded: a plan revision, a new thought
  and a new process all arrive without moving a section that was closed
  by hand. The card itself folds the same way:
  its header is one button that folds the whole overlay down to itself
  — and shrinks it to the header's own width — while the dot goes on
  pulsing in it, so the corner comes back without losing the report
  that work is running; nothing that arrives re-opens a folded card
  either — the reader's folds last as long as the conversation, since it
  is a session change rather than a turn that remounts the card — and
  unfolding brings the body back at the sections' own defaults, because
  a folded card renders no body for a fold made inside it to survive in.
  The process section is about live work: it lists the processes still
  running (newest first, four rows plus the selected one) with the tail
  of the selected one, and a process that stops drops off the card at
  the next read — the exit status and the output it printed are the
  transcript's record. Output is shown as the process wrote it: nothing
  is redacted and nothing is inferred, which is also why the card offers
  no actions — stopping a process stays a tool decision the model makes,
  and the transcript's own session cards remain the place to act on one.
- A conversation's sandboxed processes are readable while they run, not
  only after the model reads them. A new `opencraft.processes` resource
  taps every session the sandbox runner starts inside a session run
  (exec_command and exec_session alike, on both sandbox backends) and
  drains its output into a bounded 16 KiB tail that `Session.Processes`
  serves to the desktop. Draining never consumes bytes — sandbox output
  logs are cursor-based and replayable, so the feed reads with its own
  cursor beside the model's — and closing a tapped session salvages
  whatever the backend still buffers, so a command that closes its
  session the moment it ends still lands complete instead of racing the
  drain. Each read is windowed to a quarter second, which keeps a tap
  from holding the process's single blocking read slot, and a read that
  brings nothing is followed by a pause, so a silent server is asked
  about twice a second rather than as fast as the backend answers. A
  backend that trimmed output faster than the feed read it freezes the
  tail and marks it truncated rather than retrying a dead cursor. The
  feed is a per-generation resource: a runtime reload (a settings save,
  a plugin install) starts on an empty feed, exactly like the sessions
  it was watching.
- A turn that is only thinking no longer commits at the pace of prose.
  Reasoning is the one stream the transcript never renders — the
  activity card's thought block is its only reader, and what it shows is
  a tail meant to be skimmed — so a queue holding nothing but reasoning
  now folds into the store at 250ms instead of the 100ms streaming text
  keeps, which is four updates a second against a burst that used to
  re-lay-out the card's largest text block ten times a second. The
  answer's own words keep the fast beat, and prose joining a thought
  pulls the queued commit in rather than waiting out the slower one.
- The sidebar folds a workspace's session list at four rows instead of
  ten. Ten rows per workspace is most of the sidebar's height once two or
  three workspaces are open, which pushes the collapsed nodes — the map
  of where the reader was — off the bottom, and the rows a workspace
  hides are one click away behind "More sessions" anyway. What the
  preview counts is unchanged: rows for running sessions are still added
  on top of it, so a workspace with a live turn shows the turn plus four
  stored rows, and "More sessions" still counts only the stored rows it
  folds away.

### Fixed

- Saving MCP servers no longer drops the rest of a hand-written `tools`
  resource. The user-layer merge that is supposed to deep-merge the
  generated keys into the existing resource inserted them into the
  document that gets discarded instead of the one that is written, so a
  layer that declared its own `tools` deps (or its `kind`/`impl`) came
  out with only the generated `tool.mcp` dep. The merged node now
  reaches the file, which is what the delegation policy's save also
  relies on: saving the limits keeps the `delegate` resource's deps and
  any hand-added key.
- The ⌘K palette opens with the caret already in its search field, and
  closing a dialog hands focus back to where it came from. The panel
  mounted one commit after the overlay that wires keyboard behaviour,
  so it found nothing to trap; and React's own focus restore undid
  the restore while a panel was still on screen for its exit animation,
  which is how the composer lost the caret every time the palette
  closed. An approval prompt that arrives while the caret is in the
  composer now owns the keyboard (nothing is pre-selected; Space picks,
  Enter answers), and answering it returns the caret to the composer.
- A generation tool's dialog no longer opens inside the settings panel
  it was launched from. The panel clips its own overflow, and its
  entrance animation — like every panel's, drawer's, popover's and
  toast's — filled forwards, which leaves the element with an identity
  matrix rather than no transform at all: a transformed panel is a
  containing block for its own `position: fixed` descendants, so the
  dialog was laid out against the panel and cut off by it. ByteDance's
  ten dotted knobs made that plain, and the header and the save bar were
  the two things the clip ate first. Entrance animations fill backwards
  now, so the settled panel carries no transform, and the dialogs the
  settings panel opens (the image and video tool cards, web search, an
  MCP server) are portalled to the document body. The tool dialog also
  grew to 42rem with a wrapping name line, so a vocabulary like
  `optimize_prompt.thinking · auto · enabled · disabled` and its
  provider-default control are read in full instead of truncated.
- The file tools work on Windows again: `read_file`, `write_file`,
  `list_dir`, `grep` and `glob` refused every nested path there.
  `validatePath` compared `filepath.Clean(p)` against `p`, and on
  Windows that rewrites `src/main.go` into backslashes, so the clean
  check rejected the slash form the tools themselves emit — while the
  backslash form is refused with a "use forward slashes" hint, leaving
  no spelling that worked below the workspace root. Cleanliness is
  judged with `path.Clean` now, and `list_dir`'s `max_depth` counts `/`
  instead of the host separator (counting the separator reads every
  walked path as depth 0, so the bound never trimmed). Windows CI runs
  this package now, which is how it was found.
- `apply_patch` no longer judges a patch path with the host separator.
  On Windows `filepath.Clean` rewrites `../x` into `..\x`, so the
  `../`-prefix check never matched there: a patch spelling an escape the
  Windows way passed the parser and was only stopped by the layer below
  (the workspace's own traversal check, or `pathsafe.RelRef` in the host
  applier). Paths are judged in the workspace namespace now — backslashes
  folded, `path.Clean`, and both readings of absolute: a leading slash
  everywhere, plus the drive and UNC forms only Windows reads that way —
  so a traversal is refused by the parser, with the parser's message,
  whichever separator spells it, and on every platform. Folding is
  for judging only: a plain Windows-style relative path keeps resolving,
  since the workspace reads the backslash as a separator itself. Windows
  CI runs the package's path tests, including the drive and UNC forms
  that only exist there.
- A process started in a turn's last poll gap is no longer invisible
  until the next message. The activity card's process section follows a
  hook that reads the conversation's sandboxed processes on open and
  then only while a turn runs or a process is known to run, so a
  session whose row landed after the turn's final read (the sandbox
  registers it at Start, and a turn can end right after its own tool
  call returns) left nothing behind to read again: nothing running was
  known, the feed stopped reading, and the still-running server stayed
  out of sight until the next turn. The hook now also reads once on the
  turn's falling edge — the row is always there by then — and a running
  answer hands the conversation to the idle pace from there.
- A sandboxed session read that stopped at its own request deadline
  reported the child's wire error (`execd: read: deadline_exceeded:
  request deadline exceeded`) instead of a timeout: the request deadline
  and the client's own context cancel race each other, and only one of
  the two outcomes looked like a timeout to a caller. The remote session
  now translates that code — and the child's cancel code — into the
  plain context errors the local backend has always returned, so a
  reader that re-polls from its cursor reads "no output yet" instead of
  a failure it has to classify itself.
- One GUI per state root no longer rests on the shell's single-instance
  machinery. The desktop app takes its own non-blocking lock on
  `<state root>/gui.lock` (the same kernel primitives as the workspace
  lock) before anything else runs, so the guarantee holds on every
  platform instead of depending on `$TMPDIR` flock semantics, a session
  bus (whose absence used to abort startup on Linux) or a named mutex.
  A rejected second launch is no longer silent either: it asks the
  holder to bring its window to the front over a per-root socket (a
  named pipe on Windows), prints one line on stderr naming the holder
  (pid, version, start time) and exits 1. A state root whose lock file
  cannot be created fails open with a warning — a broken lock must not
  keep a user out of their own app — and
  `OPENCRAFT_NO_SINGLE_INSTANCE=1` still skips the gate entirely. The
  shell's single-instance options stay enabled as a second,
  opportunistic gate, which is what keeps launches from older builds of
  the same root lineage in check. The lock is keyed on the state root,
  exactly like the instance id: `--profile dev` (what `wails3 task dev`
  runs as) keeps its own root and therefore still starts beside the
  installed app, and a raise aimed at one root never touches the other
  window. Only a second instance on the *same* root is rejected - the
  installed app opened twice, or a dev build pointed at the installed
  app's root without a profile.
- A correction typed while a scheduled automation runs in the open
  workspace is reported like one typed into an interactive turn: the
  automation's terminal event carries the undelivered-steer count, so
  the text becomes a resend/dismiss card instead of vanishing from the
  transcript on the next archive reconciliation. (#181)
- A wait cancelled before its turn settled no longer crashes the turn
  goroutine: the settle path tolerates a result-less wait, so the turn
  still archives and the caller still sees the cancellation. (#181)
- The `automation` tool's nested `task`/`schedule` schema now reaches the
  model intact. Nested properties were built as `ToolPropertyDef`
  values, whose schema fields are unexported, so each one marshalled as
  an empty object: the model saw property names with no type and no
  description — including the run-limit field — and no allowed values
  for the schedule kind. Nested properties are raw JSON Schema maps
  now, like every other tool in the repo.
- A staged draft (the Tab queue, or Enter pressed while the previous
  send was still starting) runs in the workspace that owns its
  conversation instead of the one on screen. The start used to resolve
  its workspace from the active window, so a draft queued behind a
  turn in another workspace was attached there as a brand-new session —
  with that workspace's working directory and sandbox mode — and the
  message never reached the conversation it was meant for. The start
  request now carries the owning workspace, a workspace the window has
  left is served by its own background Host (the UI's current Host is
  never taken over), and a start for a conversation no store owns is
  refused rather than minted somewhere.
- A completion notification that races a workspace store close (runtime
  rebuild, workspace switch, shutdown) no longer logs a spurious
  `sql: database is closed`: a store that closed between the banner's
  pre-check and its title read falls back to the app name like the
  pre-check already intended, and only a store that was still open
  reports the read as an error.
- A settings write that changes nothing no longer invalidates the
  runtime: a Host that is stale but already superseded counts as serving
  its workspace, because its replacement reads the same document. A
  plugin that re-submits its unchanged inference rows on every catalog
  sync used to rebuild once per row while the workspace drained, each
  rebuild arming another replacement for the same document.
- The web-search tool and the page fetcher work under a wrapped process
  transport (the round-trip probe above, or an SDK's tracing hook): both
  cloned `http.DefaultTransport` through an unchecked
  `*http.Transport` assertion, which panics on a wrapper. They apply the
  same guard the official provider SDKs use, and the fetcher's default
  client leaves `Transport` nil so its requests stay visible to the
  probe.
- The macOS traffic lights stay on the chat header. The 4pt nudge that
  lines the three window buttons up with the 44pt header strip ran once
  per page load and resolved its target as "the first window the app
  owns", so any frame change AppKit re-laid out (a resize, the zoom
  button, leaving fullscreen) dropped them back to its 26pt default
  until the next load. The nudge now takes the main window's native
  handle from the Wails window and is re-applied from a window-layout
  observer, skipped while fullscreen so macOS keeps its own placement
  there.
- An open menu no longer closes because a scroller somewhere else in the
  window moved. The popover dismissed itself on any scroll event in the
  capture phase — the rule exists because a scroll invalidates the
  measured anchor — and then closed whenever the scrolled node was not
  inside its own panel, which is every pane the menu is not anchored in:
  the chat transcript following a turn, the activity card re-pinning its
  thought and process tails as they stream. A settings dropdown closed
  under the reader's cursor the moment the model printed its next
  thought. The dismissal now fires only for the page itself and for a
  container the anchor is inside, which is the case that actually moved
  it, while the panel's own list keeps scrolling itself as before.
  Hover hints are pinned to a control the same way and now follow the
  same rule instead of vanishing whenever anything in the window moved.
- A text block the model had finished writing no longer sits on screen in
  raw markdown until its turn ends. The transcript chose between plain
  text and markdown once per message, with a flag that meant "this turn's
  answer is still streaming", so every block of a running turn rendered
  its markers literally: `## Plan` stayed `## Plan`, and the heading font
  arrived only when the whole turn settled and the pane re-laid itself
  out. The flag now marks the one item it was meant for — the message's
  trailing block, while its turn runs — so a block that a tool call (or
  the next block) ended is parsed the moment it stops growing. Only the
  block still being written stays plain text, which is what keeps a
  half-written `#` from being re-parsed on every delta.
- An `ask_user` card's header no longer loses its question to a long
  answer. The answer chip only ever grew — a multi-choice answer joined
  its option texts — and the question was the one part of the row that
  could give way, so three options of ordinary length squeezed it to
  nothing and painted both past the card edge, with no ellipsis anywhere
  because nothing was capped. A multi-choice answer is now named by its
  count (`✓ 3 choices`), the chip is capped to its share of the row and
  truncates, and the answer it had to shorten is one hover away and
  spelled out in the expanded body, where every option is still listed
  with the ticked ones marked.

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
