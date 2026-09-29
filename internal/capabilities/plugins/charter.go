package plugins

// This file is the plugin framework's charter: the two tables that
// answer, for everything a plugin can give the host or call on it, the
// five questions every new mechanism used to answer for itself:
//
//  1. Who consumes it?             Consumer
//  2. Where does the code run?     Runtime
//  3. How does the host learn it?  Declaration
//  4. What authorizes it?          Grant
//  5. When does it live and die?   Activation, Teardown
//
// Those five answer what a contribution is. The register clock
// (FaceRefreshes) answers the sixth question the columns cannot answer
// on their own: when an answer has to be re-read. Every successful
// registry mutation moves Store's revision; each consumer face watches
// it, and the activation cells describe what the next read therefore
// sees.
//
// The dividing line the tables draw is by consumer, not by taste. The
// machine half — what the agent runtime and (one day) the app platform
// consume — is declared in the manifest, because those consumers must
// enumerate it without executing plugin code. The UI half is registered
// at runtime, because a UI contribution *is* code: a manifest copy of it
// could only drift. The three copies the manifest used to carry
// (contributes.settingsPanels, contributes.sidebarEntries,
// contributes.pets) were parsed and rendered by nothing, and the P3
// cleanup deleted them: a manifest that still writes the segment keeps
// loading, the segment is ignored (LegacyManifestInputs).
//
// The tables are not prose. charter_test.go scans the code each row
// points at — the Manifest struct, AllowedPermissions, the frontend's
// service list, the kraft primitive dispatch — and fails in both
// directions: a permission, manifest field, service or primitive that no
// row claims, and a row that names something which no longer exists.
// There is no third state except the two this file names: a row that is
// not current is legacy or reserved, and says in its Note what has to
// change for it to leave; the scan keeps it honest until it does.
//
// The rendered view of these tables is charter.md beside this file; it
// is generated (go test -run TestCharterDocument -update) and checked
// in, because a reviewer reads a diff, not a struct literal.

// CharterConsumer names the surface that consumes a contribution.
type CharterConsumer string

const (
	// ConsumerUI is the desktop shell itself: settings panels, sidebar
	// entries, commands, the status bar and the pet window.
	ConsumerUI CharterConsumer = "ui"
	// ConsumerAgent is the agent runtime: assembly turns the declaration
	// into skills, hooks, MCP servers and tools.
	ConsumerAgent CharterConsumer = "agent"
	// ConsumerPlatform is the app platform, which will schedule nodes
	// through the same manifest half the agent uses (reserved).
	ConsumerPlatform CharterConsumer = "platform"
)

// CharterDeclaration is how the host learns a contribution exists.
type CharterDeclaration string

const (
	// DeclaredInManifest is read from plugin.json without running any
	// plugin code.
	DeclaredInManifest CharterDeclaration = "manifest"
	// DeclaredByRegistration is the plugin's own bundle registering
	// itself while it runs.
	DeclaredByRegistration CharterDeclaration = "registration"
)

// CharterRuntime is where a contribution's code runs.
type CharterRuntime string

const (
	// RuntimeWebview is the plugin's ES module inside a webview.
	RuntimeWebview CharterRuntime = "webview"
	// RuntimeKraft is the plugin's declared subprocess.
	RuntimeKraft CharterRuntime = "kraft"
	// RuntimeSubprocess is a process the host spawns or connects to
	// from the manifest's own description (MCP servers), not the
	// plugin's kraft.
	RuntimeSubprocess CharterRuntime = "subprocess"
	// RuntimeData is no code at all: files or values the host reads.
	RuntimeData CharterRuntime = "data"
)

// CharterStatus separates what the tables promise from what they only
// still tolerate.
type CharterStatus string

const (
	// StatusCurrent is the framework as it stands.
	StatusCurrent CharterStatus = "current"
	// StatusReserved is wired to nothing on purpose: the name is kept so
	// the eventual implementation lands as a row plus an adapter rather
	// than as a new special case.
	StatusReserved CharterStatus = "reserved"
)

// ContributionKind is one row of the contribution table: a thing a
// plugin gives the host.
type ContributionKind struct {
	// ID names the row, "<consumer>.<thing>" — "ui.panel", "agent.tool".
	ID string
	// Summary describes it in one line.
	Summary     string
	Consumer    CharterConsumer
	Declaration CharterDeclaration
	Runtime     CharterRuntime
	// Activation is when it comes alive.
	Activation string
	// Teardown is what removes it.
	Teardown string
	// Grant is the permission the declaration requires, "" when the
	// contribution costs nothing to declare.
	Grant string
	// Services are the frontend registration keys the kind registers
	// through (registration kinds only).
	Services []string
	// ManifestPaths are the manifest JSON prefixes that declare the kind
	// (manifest kinds only). A path covers everything under it.
	ManifestPaths []string
	// Bound is the per-plugin size the host enforces on this kind's
	// declaration, empty for a kind the manifest does not declare. The
	// limits are per plugin: nothing bounds how many plugins are
	// installed, and what reaches the model is bounded per round by the
	// deployment instead (see the generated document's introduction).
	Bound  string
	Status CharterStatus
	// Note carries what a reader needs beyond the columns: why a
	// non-current row is still here, or where the implementation and
	// the promise disagree today.
	Note string
}

// HostInterface is one row of the interface table: something the host
// offers to plugin code. The five questions read in the plugin's
// direction — the consumer is the plugin, and activation is the moment
// the host answers.
type HostInterface struct {
	// ID names the row: a service key ("storage") or a primitive family
	// ("secret.*").
	ID string
	// Summary describes it in one line.
	Summary string
	Surface CharterSurface
	// Services are the Cordis service or accessor keys this row is
	// reachable through (webview surface only).
	Services []string
	// Methods are the JSON-RPC primitives this row covers (kraft surface
	// only).
	Methods []string
	// Grant is the permission the manifest must declare to use it, ""
	// when the call is free.
	Grant string
	// Checks name where that grant is actually spent. A row that
	// declares a grant without naming its check fails the scan, which
	// is the failure this table exists to prevent: a row that claims a
	// gate nothing enforces reads exactly like one that is enforced.
	// One check per gated primitive family when a row covers several.
	Checks []GrantCheck
	// Requires names the other rows a caller must be able to use for
	// this one to work at all — inference.upsert stores the row's
	// credential through secret.*, so a plugin that wants that flow
	// declares both grants. The scan resolves every name to a row.
	Requires []string
	// Scope is what the call can touch once it is allowed.
	Scope  string
	Status CharterStatus
	Note   string
}

// GrantCheck is one place a grant is spent: either a handler in the
// kraft runtime, or the host-side function a primitive delegates to.
// Exactly one of the two shapes is filled.
type GrantCheck struct {
	// Handler is the kraft.go function that calls requirePermission
	// for the row's grant.
	Handler string
	// File and Func name the host-side check: File is read and Func
	// must call pluginHasPermission with the row's grant. The
	// primitives that delegate (session.import, telemetry.configure)
	// gate inside the desktop core handler they call.
	File string
	Func string
}

// Empty reports whether the check names nothing, which a row that
// declares a grant may not do.
func (g GrantCheck) Empty() bool {
	return g.Handler == "" && g.Func == ""
}

// Describe renders the check for the generated document.
func (g GrantCheck) Describe() string {
	switch {
	case g.Handler != "":
		return "`kraft.go:" + g.Handler + "`"
	case g.Func != "":
		return "`" + g.File + ":" + g.Func + "`"
	}
	return "—"
}

// CharterSurface is where a host interface is reachable.
type CharterSurface string

const (
	// SurfaceWebview is a Cordis service on the plugin's context.
	SurfaceWebview CharterSurface = "webview"
	// SurfaceKraft is a JSON-RPC primitive on the plugin's subprocess.
	SurfaceKraft CharterSurface = "kraft"
)

// FaceRefresh is one row of the register clock: what one consumer face
// does when it sees the registry revision behind the one it last read
// from. The five questions answer what a contribution is; the clock
// answers when the answer has to be re-read.
type FaceRefresh struct {
	// Face names the consumer — the ContributionKind consumers, plus
	// the plugin subprocess, whose lifecycle the mutation itself ends.
	Face string
	// What is what the face re-reads, and by which mechanism.
	What string
}

// FaceRefreshes is the clock table. Its honest limit: the revision is
// per process, so it clocks the in-process consumers. A change made
// outside Store (a directory dropped into the registry by hand) moves
// nothing and is picked up by the next full rebuild, not by the clock.
var FaceRefreshes = []FaceRefresh{
	{
		Face: string(ConsumerUI),
		What: "reloads — the settings page re-runs its plugin load cycle " +
			"after the mutation returns, and Core.RefreshPluginRuntime " +
			"rebuilds the runtime once per revision, coalescing a burst " +
			"into one rebuild",
	},
	{
		Face: string(ConsumerAgent),
		What: "re-scans — the agent host caches its plugin scan against " +
			"the revision and re-reads it on the next call after it " +
			"moves; the tool source subscribes instead (Host.Watch) and " +
			"republishes its kraft set into the live registry, so a tool " +
			"gained or lost mid-turn reaches the next round; the sources " +
			"that only snapshot at assembly (skills, hooks, MCP) still " +
			"pick a change up at the next assembly",
	},
	{
		Face: "kraft",
		What: "is killed — disable, update, rollback and uninstall stop " +
			"the plugin's subprocess before anything re-reads the " +
			"manifest it was started from",
	},
	{
		Face: string(ConsumerPlatform),
		What: "nothing yet — the face lands with the node row " +
			"(platform.node) and watches the same revision",
	},
}

// ContributionKinds is the contribution table. Adding a row is how the
// framework grows a kind; charter_test.go refuses a kind the code does
// not back, and a code path no row backs.
var ContributionKinds = []ContributionKind{
	{
		ID:          "ui.panel",
		Summary:     "a settings panel",
		Consumer:    ConsumerUI,
		Declaration: DeclaredByRegistration,
		Runtime:     RuntimeWebview,
		Activation: "the plugin bundle loads and apply() calls " +
			"ctx.settingsPanels.add",
		Teardown: "the registration's disposer, which runs when the plugin " +
			"scope ends: disable, update, unload or app teardown",
		Services: []string{"settingsPanels"},
		Status:   StatusCurrent,
	},
	{
		ID:          "ui.entry",
		Summary:     "a sidebar entry",
		Consumer:    ConsumerUI,
		Declaration: DeclaredByRegistration,
		Runtime:     RuntimeWebview,
		Activation: "the plugin bundle loads and apply() calls " +
			"ctx.sidebarEntries.add",
		Teardown: "the registration's disposer (disable, update, unload, " +
			"app teardown)",
		Services: []string{"sidebarEntries"},
		Status:   StatusCurrent,
	},
	{
		ID:          "ui.command",
		Summary:     "a command in the command palette",
		Consumer:    ConsumerUI,
		Declaration: DeclaredByRegistration,
		Runtime:     RuntimeWebview,
		Activation: "the plugin bundle loads and apply() calls " +
			"ctx.commands.add",
		Teardown: "the registration's disposer (disable, update, unload, " +
			"app teardown)",
		Services: []string{"commands"},
		Note: "commands:register was retired by the vocabulary sweep: " +
			"every plugin may register commands, and the name has gated " +
			"nothing since the Cordis port. A manifest that still " +
			"declares it is accepted and the name ignored.",
		Status: StatusCurrent,
	},
	{
		ID:          "ui.status",
		Summary:     "a status-bar widget",
		Consumer:    ConsumerUI,
		Declaration: DeclaredByRegistration,
		Runtime:     RuntimeWebview,
		Activation: "the plugin bundle loads and apply() calls " +
			"ctx.statusBar.add",
		Teardown: "the registration's disposer (disable, update, unload, " +
			"app teardown)",
		Services: []string{"statusBar"},
		Note: "statusbar:contribute was retired by the vocabulary sweep, " +
			"for the same reason as commands:register.",
		Status: StatusCurrent,
	},
	{
		ID:          "ui.pet",
		Summary:     "a declarative pet pack (Rive specs) for the pet window",
		Consumer:    ConsumerUI,
		Declaration: DeclaredByRegistration,
		Runtime:     RuntimeWebview,
		Activation: "the bundle loads and calls ctx.pets.add; the Go " +
			"registry validates the pack before any pet window mounts it",
		Teardown: "the disposer unregisters the pack and restores the " +
			"builtin it overrode",
		Services: []string{"pets"},
		Note: "pets:contribute was retired with the manifest copy of the " +
			"segment it guarded; packs arrive through the registrar, and " +
			"that path checks no permission.",
		Status: StatusCurrent,
	},
	{
		ID:          "agent.skill",
		Summary:     "a directory of agent skills, mounted as a skill root",
		Consumer:    ConsumerAgent,
		Declaration: DeclaredInManifest,
		Runtime:     RuntimeData,
		Activation: "the next runtime assembly after the plugin is " +
			"enabled; assembly reads enabled plugins' manifests",
		Teardown: "the next assembly after disable or update drops the " +
			"root; a turn already running keeps the skills it assembled",
		Grant:         "skills:provide",
		ManifestPaths: []string{"skills"},
		Bound: "32 entries per plugin, 256-byte paths; a plugin that " +
			"declares none contributes its default skills/ directory",
		Status: StatusCurrent,
	},
	{
		ID:          "agent.hook",
		Summary:     "a hooks.json the runtime adds as an extra source",
		Consumer:    ConsumerAgent,
		Declaration: DeclaredInManifest,
		Runtime:     RuntimeData,
		Activation:  "the next runtime assembly after the plugin is enabled",
		Teardown: "the next assembly after disable or update drops the " +
			"hooks; hooks the runtime already registered keep running for " +
			"the turn in flight",
		Grant:         "hooks:provide",
		ManifestPaths: []string{"hooks"},
		Bound:         "16 entries per plugin, 256-byte paths",
		Status:        StatusCurrent,
	},
	{
		ID:          "agent.mcp",
		Summary:     "an MCP server the agent may call",
		Consumer:    ConsumerAgent,
		Declaration: DeclaredInManifest,
		Runtime:     RuntimeSubprocess,
		Activation: "the next runtime assembly, which hands the servers " +
			"to the MCP source; relative stdio commands resolve against " +
			"the plugin directory",
		Teardown: "the next assembly after disable or update re-reads the " +
			"manifest; a rebuilt runtime closes the connection",
		Grant:         "mcp:provide",
		ManifestPaths: []string{"mcpServers"},
		Bound: "16 servers per plugin; 1024-byte command, 2048-byte " +
			"url, 32 env entries per server",
		Status: StatusCurrent,
	},
	{
		ID:          "agent.tool",
		Summary:     "an agent-callable tool backed by a kraft method",
		Consumer:    ConsumerAgent,
		Declaration: DeclaredInManifest,
		Runtime:     RuntimeKraft,
		Activation: "the assembly that scans the manifest lists the tool, " +
			"and the tool source republishes on the registry's own signal " +
			"after every mutation, so a plugin installed, updated or " +
			"disabled mid-turn is callable — or not — from that turn's " +
			"next round; the kraft process itself starts lazily on the " +
			"first call",
		Teardown: "the next republish drops the spec; the process is " +
			"stopped when the plugin is disabled, updated or unloaded",
		Grant:         "tools:provide",
		ManifestPaths: []string{"tools"},
		Bound: "64 tools per plugin; 1024-character description, 32 KiB " +
			"input schema, 128-byte method per tool. The sum is bounded " +
			"too: the agent source caps the whole registry at 256 tools " +
			"and 512 KiB of definitions " +
			"(capabilities/tools/pluginagent/source.go: maxKraftTools, " +
			"maxKraftDefinitionBytes), keeps what fits in registry order " +
			"and logs one line naming what it dropped. Below that, what " +
			"the model sees per round is the deployment's visible and " +
			"discovery budgets (core's visibleCandidates / " +
			"fitDiscoveryPool), which skip an oversized definition " +
			"instead of starving the ones behind it. A result comes back " +
			"verbatim: the ceilings on it are the deployment's tool " +
			"middleware (truncate, redact, audit)",
		Note: "the result is a plugin's own text, and the framework's " +
			"<opencraft-context> delimiter is not escaped in it: a plugin " +
			"that echoes one writes something the model reads as injected " +
			"context. Every tool result shares this, so the exit is one " +
			"rule in the tool-result middleware (like redact or " +
			"truncate), not a special case here.",
		Status: StatusCurrent,
	},
	{
		ID:          "platform.node",
		Summary:     "a compute node the app platform may schedule (not implemented)",
		Consumer:    ConsumerPlatform,
		Declaration: DeclaredInManifest,
		Runtime:     RuntimeKraft,
		Activation:  "not implemented",
		Teardown:    "not implemented",
		Status:      StatusReserved,
		Note: "not implemented: no manifest field, no consumer, no grant. " +
			"The row is here on purpose, so the node kind arrives as a " +
			"row plus an adapter instead of a seventh special case.",
	},
}

// HostInterfaces is the interface table: everything plugin code may
// call on the host, with the grant and the scope it costs.
var HostInterfaces = []HostInterface{
	{
		ID:       "react",
		Summary:  "the host's React instance",
		Surface:  SurfaceWebview,
		Services: []string{"react"},
		Scope: "the same module the app renders with, so plugin " +
			"components share hooks and the reconciler",
		Status: StatusCurrent,
	},
	{
		ID:       "ui",
		Summary:  "transient status flashes and the native folder picker",
		Surface:  SurfaceWebview,
		Services: []string{"ui"},
		Scope:    "the shell's own status line, and a picker the user drives",
		Status:   StatusCurrent,
	},
	{
		ID:       "host",
		Summary:  "read-only host metadata",
		Surface:  SurfaceWebview,
		Services: []string{"host"},
		Scope:    "the running app's version string",
		Status:   StatusCurrent,
	},
	{
		ID:       "i18n",
		Summary:  "the host's current language",
		Surface:  SurfaceWebview,
		Services: []string{"i18n"},
		Scope: "the language tag only; plugins ship their own dictionaries " +
			"and render through it",
		Status: StatusCurrent,
	},
	{
		ID:       "invoke",
		Summary:  "calls a method on the calling plugin's own kraft",
		Surface:  SurfaceWebview,
		Services: []string{"invoke"},
		Scope: "the plugin's own subprocess; the host routes by method " +
			"name and interprets nothing",
		Status: StatusCurrent,
	},
	{
		ID:       "storage",
		Summary:  "the plugin's own key/value store",
		Surface:  SurfaceWebview,
		Services: []string{"storage"},
		Grant:    "storage:kv",
		Scope:    "one namespace per plugin, for non-secret data",
		Status:   StatusCurrent,
	},
	{
		ID:       "secrets",
		Summary:  "existence and delete for secrets in the auth scope",
		Surface:  SurfaceWebview,
		Services: []string{"secrets"},
		Grant:    "secrets:auth",
		Scope: "auth/<plugin>/... only; a value never crosses into the " +
			"webview",
		Status: StatusCurrent,
	},
	{
		ID:      "secret.*",
		Summary: "read, write and delete the plugin's own secrets",
		Surface: SurfaceKraft,
		Methods: []string{"secret.get", "secret.set", "secret.delete"},
		Grant:   "secrets:auth",
		Checks: []GrantCheck{
			{Handler: "handleSecret"},
		},
		Scope: "auth/<plugin>/... and inference/<plugin>/... only; the " +
			"namespace prefix narrows what the grant already allows",
		Status: StatusCurrent,
		Note: "the grant is checked when the primitive runs, the same " +
			"check the webview ctx.secrets surface passes; P2 wired it " +
			"into handleSecret, so kraft.go's package comment no longer " +
			"claims a gate that does not exist.",
	},
	{
		ID:      "open.url",
		Summary: "opens a URL in the OS browser",
		Surface: SurfaceKraft,
		Methods: []string{"open.url"},
		Scope: "hosts listed in kraft.hosts only — the manifest carries " +
			"the allowlist",
		Status: StatusCurrent,
	},
	{
		ID:       "inference.*",
		Summary:  "registers or removes a provider profile",
		Surface:  SurfaceKraft,
		Methods:  []string{"inference.upsert", "inference.remove"},
		Requires: []string{"secret.*"},
		Scope: "rows whose credential lives in the plugin's own secret " +
			"namespace (see secret.*: storing that credential needs " +
			"secrets:auth as well); the user-owned enabled flag is not " +
			"the plugin's to set",
		Status: StatusCurrent,
	},
	{
		ID:      "session.import",
		Summary: "imports a transcript bundle into a workspace",
		Surface: SurfaceKraft,
		Methods: []string{"session.import", "session.imported_sources"},
		Grant:   "sessions:import",
		Checks: []GrantCheck{
			{File: "internal/adapters/desktop/core/core_session_import.go",
				Func: "handlePluginSessionImport"},
			{File: "internal/adapters/desktop/core/core_session_import.go",
				Func: "handlePluginSessionImportedSources"},
		},
		Scope: "writes a conversation the host then owns; repeated " +
			"bundles dedupe by source. The import itself is bounded by " +
			"the caller (one bundle, 128 MiB); what the imported " +
			"conversation then costs a turn's context is bounded by " +
			"nothing here — the host writes it, the deployment's budgets " +
			"and the turn's own compaction decide what it costs",
		Status: StatusCurrent,
	},
	{
		ID:      "workspace.current",
		Summary: "the active workspace path",
		Surface: SurfaceKraft,
		Methods: []string{"workspace.current"},
		Scope:   "the path only, read on every call",
		Status:  StatusCurrent,
	},
	{
		ID:      "telemetry.*",
		Summary: "points the app's OTLP export at the plugin's collector",
		Surface: SurfaceKraft,
		Methods: []string{"telemetry.configure", "telemetry.disable"},
		Grant:   "telemetry:export",
		Checks: []GrantCheck{
			{File: "internal/adapters/desktop/core/telemetry.go",
				Func: "handlePluginTelemetryConfigure"},
		},
		Scope: "one plugin sink at a time, dropped when the plugin is " +
			"disabled or dies; header values stay in host memory",
		Status: StatusCurrent,
	},
	{
		ID:      "emit.event",
		Summary: "forwards a plugin event to the host bus",
		Surface: SurfaceKraft,
		Methods: []string{"emit.event"},
		Scope:   "nothing yet",
		Status:  StatusReserved,
		Note: "accepted and discarded: the host event bus has no " +
			"plugin-facing wiring. The row exists so the dispatch's case " +
			"is claimed; wire it or delete it.",
	},
}

// LegacyAction is what the host does with a manifest input written
// against an older vocabulary.
type LegacyAction string

const (
	// LegacyTranslate rewrites the input to its current spelling; the
	// plugin keeps loading either way.
	LegacyTranslate LegacyAction = "translate"
	// LegacyIgnore accepts the input and acts on nothing; the host
	// logs it once per process.
	LegacyIgnore LegacyAction = "ignore"
	// LegacyReject refuses the manifest with a message that names the
	// ambiguity.
	LegacyReject LegacyAction = "reject"
)

// LegacyManifestInput is one older spelling a manifest may still carry,
// and its disposition. The table is the framework's answer to "what
// happens to my manifest after the vocabulary changed?": one row per
// input, not a paragraph. Names that contain ":" are permission names
// and are cross-checked against PermissionRenames and
// RetiredPermissions by charter_test.go; key spellings and rejection
// shapes are covered by the manifest's own tests.
type LegacyManifestInput struct {
	// Name is the input, spelled the way a manifest writes it.
	Name string
	// Action is the disposition for an installed plugin: the registry
	// load path, the agent host's scan, the kraft lookup.
	Action LegacyAction
	// Authoring is the disposition for the gates an author uses —
	// Inspect, Install, Update, Rollback — where the manifest can be
	// fixed instead of read. Empty means the same as Action.
	Authoring LegacyAction
	// Target is the current spelling for LegacyTranslate rows.
	Target string
	// Note says why this is the disposition.
	Note string
}

// AtAuthoring returns the disposition the authoring gates apply.
func (l LegacyManifestInput) AtAuthoring() LegacyAction {
	if l.Authoring != "" {
		return l.Authoring
	}
	return l.Action
}

// LegacyManifestInputs is the disposition table for older manifest
// spellings: translate (the plugin keeps loading under the new name),
// ignore (accepted, dropped, logged once) or reject (two spellings at
// once is ambiguity, not compatibility). The one input whose two
// audiences differ is a manifest that writes the same thing twice: an
// installed plugin keeps loading, an offered source is refused.
var LegacyManifestInputs = []LegacyManifestInput{
	{
		Name:   "capability",
		Action: LegacyTranslate,
		Target: "kraft",
		Note: "the subprocess section was renamed; the host reads the " +
			"old key so installed plugins keep loading without an edit.",
	},
	{
		Name:      "capability + kraft, same section",
		Action:    LegacyTranslate,
		Authoring: LegacyReject,
		Target:    "kraft",
		Note: "a manifest written to load on hosts of both vintages. " +
			"Installed, the kraft key is read and the redundant old one " +
			"is logged once; offered to Inspect/Install/Update, the pair " +
			"is refused so the author deletes one.",
	},
	{
		Name:   "capability + kraft, different sections",
		Action: LegacyReject,
		Note: "two spellings that name different binaries is a manifest " +
			"bug rather than a spelling, so both audiences refuse it and " +
			"the error names both keys.",
	},
	{
		Name:   "skills:contribute",
		Action: LegacyTranslate,
		Target: "skills:provide",
		Note: "the vocabulary sweep spells contribution grants " +
			"kind:provide; the old spelling is translated silently.",
	},
	{
		Name:   "hooks:register",
		Action: LegacyTranslate,
		Target: "hooks:provide",
		Note: "the vocabulary sweep spells contribution grants " +
			"kind:provide; the old spelling is translated silently.",
	},
	{
		Name:   "mcp:contribute",
		Action: LegacyTranslate,
		Target: "mcp:provide",
		Note: "the vocabulary sweep spells contribution grants " +
			"kind:provide; the old spelling is translated silently.",
	},
	{
		Name:   "tools:expose",
		Action: LegacyTranslate,
		Target: "tools:provide",
		Note: "the vocabulary sweep spells contribution grants " +
			"kind:provide; the old spelling is translated silently.",
	},
	{
		Name:      "old + new spelling of one permission",
		Action:    LegacyTranslate,
		Authoring: LegacyReject,
		Target:    "the replacement spelling",
		Note: "the two spellings grant identical authority, so an " +
			"installed plugin is read as the canonical name and the " +
			"redundant one is logged once; offered to Inspect/Install/" +
			"Update, the pair is refused so the author deletes one.",
	},
	{
		Name:   "events:subscribe",
		Action: LegacyIgnore,
		Note: "the Cordis event bus is always available (ctx.on); the " +
			"name gates nothing, so the manifest is accepted and the " +
			"name is dropped and logged once.",
	},
	{
		Name:   "commands:register",
		Action: LegacyIgnore,
		Note: "the commands registrar is provided to every plugin, so " +
			"the manifest is accepted and the name is dropped and " +
			"logged once.",
	},
	{
		Name:   "statusbar:contribute",
		Action: LegacyIgnore,
		Note: "the status-bar registrar is provided to every plugin, so " +
			"the manifest is accepted and the name is dropped and " +
			"logged once.",
	},
	{
		Name:   "pets:contribute",
		Action: LegacyIgnore,
		Note: "it gated only the contributes.pets manifest copy, which " +
			"nothing rendered and the cleanup deleted; packs register " +
			"from the bundle, so the name is dropped and logged once.",
	},
	{
		Name:   "contributes",
		Action: LegacyIgnore,
		Note: "the manifest copy of the UI half. Panels, sidebar entries " +
			"and pet packs register from the bundle " +
			"(ctx.settingsPanels.add / ctx.sidebarEntries.add / " +
			"ctx.pets.add), so the segment is dropped; a non-empty one " +
			"is logged once.",
	},
}

// ManifestSkeleton is every manifest path that carries no contribution,
// with the reason it is there. The mirror test walks the Manifest
// struct and refuses a JSON field that is neither listed here nor an
// anchor of a contribution row, so a new field cannot appear without a
// decision about what consumes it.
var ManifestSkeleton = map[string]string{
	"id": "identity, and the namespace of everything the plugin owns: " +
		"KV entries, secrets, pet packs",
	"name":           "display name",
	"version":        "release identity, and what an update compares against",
	"minHostVersion": "the release gate the host checks before loading",
	"entry": "the ES module the shell loads; the file is the entry, the " +
		"contributions are registrations",
	"permissions": "the grants the plugin asks for; the closed set is " +
		"AllowedPermissions plus the retired names canonicalPermissions " +
		"drops, with older spellings translated (LegacyManifestInputs)",
	"update": "where the host looks for a newer version",
	"kraft": "the subprocess runtime the machine-half contributions run " +
		"in: binary, protocol, and the open.url allowlist",
}
