// Package resourcekind is the frozen list of resource kinds this repo
// declares, plus the spelling rule a new one has to follow.
//
// A resource kind is a cross-version contract, not an internal name: a
// deployment document writes it (`kind: opencraft.skills`), a user layer
// may ask for it years later, `foundation/compat` reads old spellings,
// and the app platform's restricted kind table matches it. Until now
// nothing checked it — every package declared its own `const
// ResourceKind` and a fourth spelling would have shipped unnoticed.
//
// The list here is the review surface: adding a kind means adding an
// entry (with an owner and a note saying what the value names), and
// resourcekind_test.go fails the build when a declaration and this list
// disagree in either direction. Rule answers the other half: a new kind
// must look like one of the two styles below, and the legacy budget
// keeps the exceptions countable.
//
// The values are wire values: nothing here may be renamed without
// checking the deployment documents and the migration steps that read
// old spellings. This package records them; it does not own them.
package resourcekind

import (
	"regexp"
	"strings"
)

// The two documented styles.
//
// The first is this repo's own: the `opencraft.` prefix, lower_snake
// segments, one or more. The prefix is what makes a kind obviously ours
// in a document that also names flowcraft's own kinds — and it is
// reserved: a value under it has to use this style, so a hurried
// `opencraft.MyThing` is not silently acceptable as "flowcraft style".
//
// The second is flowcraft's `<domain>.<Role>`: a lowercase domain and a
// CamelCase role (`session.Store`, `tool.Source`, `agent.Engine`) — plus
// the hook slots, whose role is a lowercase word (`hook.prepare`,
// `hook.commit`). Those two are separate patterns rather than one loose
// one, because "lowercase word after the dot" is only flowcraft's
// vocabulary for `hook.*`; letting it through everywhere would accept
// `session.store` next to `session.Store`, which is the drift this rule
// is here to stop.
const (
	OwnedPattern     = `^opencraft\.[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`
	FlowcraftPattern = `^[a-z][a-z0-9_]*\.[A-Z][A-Za-z]*$`
	HookSlotPattern  = `^hook\.[a-z][a-z0-9_]*$`
)

var (
	ownedPattern     = regexp.MustCompile(OwnedPattern)
	flowcraftPattern = regexp.MustCompile(FlowcraftPattern)
	hookSlotPattern  = regexp.MustCompile(HookSlotPattern)
)

// Style names the shape of value for error messages and review notes:
// "owned", "flowcraft", or "" for a spelling that follows neither.
func Style(value string) string {
	switch {
	case strings.HasPrefix(value, "opencraft."):
		if ownedPattern.MatchString(value) {
			return "owned"
		}
		return ""
	case ownedPattern.MatchString(value):
		return "owned"
	case flowcraftPattern.MatchString(value) || hookSlotPattern.MatchString(value):
		return "flowcraft"
	default:
		return ""
	}
}

// Matches reports whether value is spelled the way a new kind must be.
// It says nothing about the inventory: a value can be rule-compliant and
// still not be a kind anyone declared.
func Matches(value string) bool { return Style(value) != "" }

// Kind is one entry of the frozen list.
type Kind struct {
	// Value is the wire spelling, exactly as a deployment document
	// writes it.
	Value string
	// Owner is the package that declares the constant.
	Owner string
	// Note says what the value names and, for the two styles, why it is
	// spelled that way. It is what a reviewer needs to decide whether a
	// change to this entry is safe.
	Note string
	// Legacy marks a spelling that predates the rule and is kept only
	// because documents already write it. Legacy entries are counted
	// against legacyBudget below.
	Legacy bool
}

// legacyBudget is how many spellings that follow neither style may
// exist. It is one — `memory` — and it is deliberately not a soft cap:
// raising this number is the point at which a reviewer has to argue that
// a new kind cannot be spelled per the rule.
const legacyBudget = 1

// Kinds is every resource kind this repo declares, in the order the
// packages appear under internal/. Keep it alphabetical by Value: the
// test compares sets, but a sorted list is what makes a diff readable.
var Kinds = []Kind{
	{
		Value: "memory",
		Owner: "capabilities/memory",
		Note: "The summary/compaction memory resource — the one bare " +
			"name in the list. Its payload is flowcraft's `memory` " +
			"kind, so the opencraft layer around it inherited the name " +
			"instead of prefixing it.",
		Legacy: true,
	},
	{
		Value: "memory.UsageObserver",
		Owner: "capabilities/memory",
		Note: "The usage observer hook. Flowcraft's `<domain>.<Role>` " +
			"shape with the only CamelCase role in the list, because " +
			"the role is a type name rather than a lower_snake word.",
	},
	{
		Value: "opencraft.agentlifecycle",
		Owner: "capabilities/agents",
		Note: "Prepares a dynamically registered subagent: carries " +
			"work_dir/user_dir as literal paths.",
	},
	{
		Value: "opencraft.artifacts",
		Owner: "capabilities/sandbox",
		Note: "The artifact observer that records files a turn wrote " +
			"into the archive.",
	},
	{
		Value: "opencraft.automations",
		Owner: "capabilities/tools/automation",
		Note: "The automation tool source. Mounted by the desktop and " +
			"headless adapters rather than by an embedded asset.",
	},
	{
		Value: "opencraft.delegation.policy",
		Owner: "capabilities/subagents",
		Note: "The delegation policy document: which subagents may be " +
			"spawned. Two segments under the prefix.",
	},
	{
		Value: "opencraft.execpolicy",
		Owner: "capabilities/execpolicy",
		Note: "The approval/escalation manager that every exec tool " +
			"depends on. Flat single word under the prefix.",
	},
	{
		Value: "opencraft.hooks",
		Owner: "capabilities/hooks",
		Note: "The hook host: one registry for every opencraft hook " +
			"implementation.",
	},
	{
		Value: "opencraft.hooks.observer",
		Owner: "capabilities/hooks",
		Note: "The hook host's own observer. Kept distinct on the wire " +
			"from `opencraft.hooks`, which is why the dotted form is " +
			"preferred over a third flat word.",
	},
	{
		Value: "opencraft.hostworkspace",
		Owner: "capabilities/sandbox",
		Note: "The mode-aware workspace over the session's persisted " +
			"permission mode — distinct from the `workspace.Workspace` " +
			"dep it wraps. Flat single word under the prefix.",
	},
	{
		Value: "opencraft.netpolicy",
		Owner: "capabilities/sandbox",
		Note:  "The network policy attached to confined exec.",
	},
	{
		Value: "opencraft.plugin_installer",
		Owner: "capabilities/tools/plugininstall",
		Note: "The plugin installer tool source. snake_case segment " +
			"under the prefix.",
	},
	{
		Value: "opencraft.plugins",
		Owner: "capabilities/plugins/agent",
		Note:  "The plugin host the skills and hooks resources depend on.",
	},
	{
		Value: "opencraft.processes",
		Owner: "capabilities/sandbox",
		Note: "The process feed that reports live child processes to " +
			"the UI.",
	},
	{
		Value: "opencraft.review",
		Owner: "capabilities/review/store",
		Note: "The review queue store. Same value as the impl string " +
			"`opencraft.review` in review/review.go — kind and impl " +
			"vocabularies are separate, and both are listed here.",
	},
	{
		Value: "opencraft.skill_lifecycle",
		Owner: "capabilities/skills/usage",
		Note: "Skill usage/lifecycle counters. snake_case segment under " +
			"the prefix.",
	},
	{
		Value: "opencraft.skills",
		Owner: "capabilities/skills",
		Note: "The skill registry: skill roots, the curator and the " +
			"skills tool source all hang off it.",
	},
	{
		Value: "opencraft.user_memory",
		Owner: "capabilities/memory/userstore",
		Note: "The user.db-backed memory store. snake_case segment " +
			"under the prefix.",
	},
	{
		Value: "opencraft.workspace",
		Owner: "capabilities/sandbox",
		Note: "The observing workspace that reports writes with their " +
			"run info. Not the same resource as " +
			"`opencraft.hostworkspace`.",
	},
	{
		Value: "session.Store",
		Owner: "capabilities/sessions",
		Note: "Flowcraft's session store kind, served here by the " +
			"`opencraft` impl.",
	},
}

// Impls is every value a `ResourceImpl` constant declares. Impl strings
// are navigation labels inside one kind, not cross-version contracts, so
// there is no rule for them — but they are still inventoried, because a
// typo in an impl is the same silent failure as a typo in a kind.
var Impls = []string{
	"keychain",
	"local",
	"opencraft.review",
	"opencraft/automation",
	"opencraft/plugininstall",
	"opencraft/plugins",
}
