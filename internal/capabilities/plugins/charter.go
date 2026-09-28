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
	// StatusLegacy is accepted for manifests and plugins written against
	// older builds, spent by nothing. The Note names the exit.
	StatusLegacy CharterStatus = "legacy"
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
	Status        CharterStatus
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
	// Scope is what the call can touch once it is allowed.
	Scope  string
	Status CharterStatus
	Note   string
}

// CharterSurface is where a host interface is reachable.
type CharterSurface string

const (
	// SurfaceWebview is a Cordis service on the plugin's context.
	SurfaceWebview CharterSurface = "webview"
	// SurfaceKraft is a JSON-RPC primitive on the plugin's subprocess.
	SurfaceKraft CharterSurface = "kraft"
)

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
		Status:        StatusCurrent,
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
		Status:        StatusCurrent,
	},
	{
		ID:          "agent.tool",
		Summary:     "an agent-callable tool backed by a kraft method",
		Consumer:    ConsumerAgent,
		Declaration: DeclaredInManifest,
		Runtime:     RuntimeKraft,
		Activation: "the next runtime assembly lists the tool; the kraft " +
			"process itself starts lazily on the first call",
		Teardown: "the next assembly drops the spec; the process is " +
			"stopped when the plugin is disabled, updated or unloaded",
		Grant:         "tools:provide",
		ManifestPaths: []string{"tools"},
		Status:        StatusCurrent,
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
		ID:      "inference.*",
		Summary: "registers or removes a provider profile",
		Surface: SurfaceKraft,
		Methods: []string{"inference.upsert", "inference.remove"},
		Scope: "rows whose credential lives in the plugin's own secret " +
			"namespace; the user-owned enabled flag is not the plugin's " +
			"to set",
		Status: StatusCurrent,
	},
	{
		ID:      "session.import",
		Summary: "imports a transcript bundle into a workspace",
		Surface: SurfaceKraft,
		Methods: []string{"session.import", "session.imported_sources"},
		Grant:   "sessions:import",
		Scope: "writes a conversation the host then owns; repeated " +
			"bundles dedupe by source",
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
	// Action is the disposition.
	Action LegacyAction
	// Target is the current spelling for LegacyTranslate rows.
	Target string
	// Note says why this is the disposition.
	Note string
}

// LegacyManifestInputs is the disposition table for older manifest
// spellings: translate (the plugin keeps loading under the new name),
// ignore (accepted, dropped, logged once) or reject (two spellings at
// once is ambiguity, not compatibility).
var LegacyManifestInputs = []LegacyManifestInput{
	{
		Name:   "capability",
		Action: LegacyTranslate,
		Target: "kraft",
		Note: "the subprocess section was renamed; the host reads the " +
			"old key so installed plugins keep loading without an edit.",
	},
	{
		Name:   "capability + kraft",
		Action: LegacyReject,
		Note: "two spellings of one manifest section is ambiguity, not " +
			"compatibility: the manifest is refused and both names are " +
			"named in the error.",
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
		Name:   "old + new spelling of one permission",
		Action: LegacyReject,
		Note: "declaring both spellings of one grant is ambiguity, not " +
			"compatibility: the manifest is refused and both names are " +
			"named in the error.",
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
