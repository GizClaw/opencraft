// Package apps is the application platform's registry and policy: the
// installed applications themselves (content root, manifest, layers),
// and the decisions the host makes before one of their layers is ever
// assembled.
//
// This file owns the static half of that policy: which resource kinds an
// application layer may declare. Everything else about an application —
// installing it, normalizing a foreign document into a layer, the
// two-pass validation pipeline — arrives with the store, and it asks
// this table rather than re-listing kinds.
package apps

// Verdict is the answer for one resource kind. A denial always carries a
// reason: it is what the import wizard and the diagnostics panel show the
// user, so it has to say what would have been deployed and why v1 does
// not allow it.
type Verdict struct {
	// Allowed reports whether an application layer may declare the kind.
	Allowed bool
	// Reason explains a denial (empty when Allowed).
	Reason string
}

// Classify returns this build's verdict for one resource kind an
// application layer declares. ok=false means the table does not know
// the kind at all — an application deployment naming it would fail at
// assembly ("no factory"), and a kind that reaches this state in the
// table is a defect the coverage test (engine's
// TestEveryKnownKindIsClassifiedForApplications) reports: every kind
// engine.KnownKinds returns has to be a row here.
//
// The decision is per kind, not per (kind, impl): an impl is navigation
// inside one kind, and v1's policy does not turn on it.
func Classify(kind string) (Verdict, bool) {
	for _, rule := range kindTable {
		if rule.kind == kind {
			return Verdict{Allowed: rule.allowed, Reason: rule.reason}, true
		}
	}
	return Verdict{}, false
}

// kindRule is one row of the table.
type kindRule struct {
	kind    string
	allowed bool
	reason  string
}

// kindTable is the v1 allow/deny table for application layers, one row
// per resource kind this build registers (engine.KnownKinds). The rows
// are exhaustive on purpose: a factory that lands without a row here is
// an execution surface an application could reach by declaration, so the
// coverage test fails instead of defaulting.
//
// Shape of the policy (the app platform plan, §2.5): an application gets
// its own event bus, workspace, session store, artifact sink, transcript
// buffer and script runtime — and nothing else. No tools, no sandbox, no
// skills/plugins/MCP, no worldstate, no delegation, no automations, no
// user-level stores. Inference is *referenced* through the host-generated
// overlay but never declared by a layer; credentials live in the shared
// keyring view the contract layer declares.
var kindTable = []kindRule{
	// The agent surface an application actually uses.
	{kind: "agent.Engine",
		allowed: true},
	{kind: "agent.ScriptRuntime",
		allowed: true},
	{kind: "agent.ScriptBindings",
		reason: "the graph's script globals are the host's to set, not an application layer's"},

	// The contract layer's own kinds: the application may name them
	// (the contract layer declares them already), which keeps a layer
	// that repeats one from being rejected for a kind it needs.
	{kind: "event.Bus",
		allowed: true},
	{kind: "memory",
		allowed: true},
	{kind: "memory.UsageObserver",
		allowed: true},
	{kind: "opencraft.artifacts",
		allowed: true},
	{kind: "opencraft.workspace",
		allowed: true},
	{kind: "session.Store",
		allowed: true},
	{kind: "workspace.Workspace",
		allowed: true},

	// Inference is the host's: the overlay carries the user's router,
	// infer assembly and provider declarations, and a layer may only
	// reference them.
	{kind: "inference.Assembly",
		reason: "inference wiring is generated from the user's settings, not declared by an application"},
	{kind: "inference.Provider",
		reason: "providers come from the user's settings; an application never carries credentials"},
	{kind: "inference.Router",
		reason: "the routing policy is the user's, and the overlay owns it"},

	// Graph vocabulary: the built-in node types are the runtime's; a
	// layer cannot register another one.
	{kind: "graph.NodeType",
		reason: "graph node types are registered by the runtime, not by an application layer"},

	// The transcript pipeline is the contract layer's: hooks are how a
	// deployment commits history, and an application layer must not be
	// able to rewire (or shadow) that path.
	{kind: "hook.commit",
		reason: "the transcript commit path belongs to the contract layer"},
	{kind: "hook.observe",
		reason: "turn observation belongs to the contract layer"},
	{kind: "hook.prepare",
		reason: "turn preparation belongs to the contract layer"},

	// Sandbox, exec and the tool containers: v1 gives applications none
	// of them (no exec, no tool catalog, no host workspace surface).
	{kind: "sandbox.Runner",
		reason: "v1 gives applications no exec"},
	{kind: "tool.Assembly",
		reason: "v1 gives applications no tool containers"},
	{kind: "tool.Registry",
		reason: "v1 gives applications no tool catalog"},
	{kind: "tool.Source",
		reason: "v1 gives applications no tool sources"},
	{kind: "opencraft.execpolicy",
		reason: "exec approvals are not available to applications"},
	{kind: "opencraft.hostworkspace",
		reason: "the mode-aware host workspace is a tool-facing surface applications do not get"},
	{kind: "opencraft.netpolicy",
		reason: "network posture is a host concern"},

	// Credentials come from the read-only keyring view the contract
	// layer declares (secret.keychain); an application layer may not
	// declare a secret store of its own.
	{kind: "secret.Store",
		reason: "credentials come from the shared keyring view, not from an application layer"},

	// Everything else that hangs off the user's assistant: skills,
	// long-term memory, review, delegation, dynamic subagents, plugin
	// hosting, automations and the process feed.
	{kind: "delegation.AsyncBackend",
		reason: "delegation is not available to applications"},
	{kind: "delegation.Directory",
		reason: "delegation is not available to applications"},
	{kind: "delegation.Service",
		reason: "delegation is not available to applications"},
	{kind: "delegation.SessionProvider",
		reason: "delegation is not available to applications"},
	{kind: "opencraft.agentlifecycle",
		reason: "dynamically registered subagents are not available to applications"},
	{kind: "opencraft.delegation.policy",
		reason: "delegation policy is a host concern"},
	{kind: "opencraft.hooks",
		reason: "the hook host belongs to the contract layer"},
	{kind: "opencraft.hooks.observer",
		reason: "the hook host belongs to the contract layer"},
	{kind: "opencraft.processes",
		reason: "the process feed is not available to applications"},
	{kind: "opencraft.review",
		reason: "post-turn review is not available to applications"},
	{kind: "opencraft.skill_lifecycle",
		reason: "skills are not available to applications"},
	{kind: "opencraft.skills",
		reason: "skills are not available to applications"},
	{kind: "opencraft.user_memory",
		reason: "long-term memory is not available to applications"},
}
