package plugins

// This file is the plugin permission vocabulary: the closed set of
// grants a manifest may declare, plus the compatibility layers every
// parsed manifest passes through — the spellings that were renamed
// (PermissionRenames), the names that were retired (RetiredPermissions),
// and the once-per-process warnings those paths share (warnOnce). The
// narrative tables live in charter.go; charter_test.go checks the two
// files against each other and against the code that spends the grants.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

// AllowedPermissions is the closed set of host capabilities a plugin
// may declare. Unknown permissions reject the plugin (fail-closed);
// older spellings never reach this set, because canonicalPermissions
// translates or drops them first.
var AllowedPermissions = map[string]bool{
	"secrets:auth":    true,
	"storage:kv":      true,
	"tools:provide":   true,
	"sessions:import": true,
	"skills:provide":  true,
	"mcp:provide":     true,
	"hooks:provide":   true,
	// telemetry:export lets a kraft plugin point OTLP export at
	// its own collector over telemetry.configure. It ships the whole
	// app log stream (prompts included) to that collector, so the host
	// records the active sink and drops it when the plugin is
	// disabled or uninstalled.
	"telemetry:export": true,
}

// CheckPermissions validates a manifest permission list. It runs on the
// canonical list canonicalPermissions returns, so it stays strictly
// fail-closed: a name that is not in AllowedPermissions rejects the
// plugin.
func CheckPermissions(perms []string) error {
	for _, p := range perms {
		if !AllowedPermissions[p] {
			return fmt.Errorf("plugins: unknown permission %q", p)
		}
	}
	return nil
}

// PermissionRename is one grant that changed spelling, and its
// replacement.
type PermissionRename struct {
	From string
	To   string
}

// PermissionRenames maps the grants renamed by the vocabulary sweep to
// the canonical spelling: a contribution is something a plugin
// *provides*, and every contribution grant says so. A manifest that
// still writes the old name keeps loading — canonicalPermissions
// translates it instead of rejecting the plugin.
var PermissionRenames = []PermissionRename{
	{From: "skills:contribute", To: "skills:provide"},
	{From: "hooks:register", To: "hooks:provide"},
	{From: "mcp:contribute", To: "mcp:provide"},
	{From: "tools:expose", To: "tools:provide"},
}

// RetiredPermission is a grant the vocabulary dropped: a manifest may
// still declare it, but canonicalPermissions removes it and the host
// logs it once. Note says what the grant was for and why it buys
// nothing any more; the charter's legacy-inputs table tells the
// manifest's story.
type RetiredPermission struct {
	Name string
	Note string
}

// RetiredPermissions is the set of permissions nothing spends any
// more, so nothing enforces them. Dropping an installed manifest over
// one would kill the plugin's working half for a name that gates
// nothing; CheckPermissions is fail-closed, so the names never touch
// AllowedPermissions again and canonicalPermissions removes them on the
// way in.
var RetiredPermissions = []RetiredPermission{
	{
		Name: "events:subscribe",
		Note: "the Cordis event bus is always available (ctx.on); no gate " +
			"was ever wired to the name.",
	},
	{
		Name: "commands:register",
		Note: "the commands registrar is provided to every plugin; the " +
			"name has been checked by nothing since the Cordis port.",
	},
	{
		Name: "statusbar:contribute",
		Note: "the status-bar registrar is provided to every plugin, for " +
			"the same reason as commands:register.",
	},
	{
		Name: "pets:contribute",
		Note: "it gated only the manifest copy of pet packs, deleted once " +
			"packs became registrations; packs arrive through ctx.pets, " +
			"and that path checks no permission.",
	},
}

// canonicalPermissions rewrites a manifest's permission list into the
// current vocabulary. Every parsed manifest runs through it, so
// validation, the plugin summary, the agent host and the kraft gate all
// read the same names. Renames are translated silently (the plugin
// works either way); a retired name is dropped and logged once per
// process, not once per parse: Store.List() and the agent host re-parse
// on every scan, and one stale manifest must not become a flood.
// Declaring an old and its replacement spelling together is the one
// audience-dependent case: the authoring gates refuse it (two names for
// one grant, and the author is present to fix it), while an installed
// manifest is read as the grant it names either way — refusing it would
// kill a plugin whose manifest was written to load on both the old and
// the new host.
func canonicalPermissions(
	id string,
	perms []string,
	audience manifestAudience,
) ([]string, error) {
	declared := make(map[string]bool, len(perms))
	for _, p := range perms {
		declared[p] = true
	}
	for _, r := range PermissionRenames {
		if declared[r.From] && declared[r.To] {
			if audience == manifestAuthored {
				return nil, fmt.Errorf(
					"plugins: permissions declare both %q and its replacement %q",
					r.From, r.To)
			}
			warnLegacyGrantPair(id, r)
		}
	}
	out := make([]string, 0, len(perms))
	seen := make(map[string]bool, len(perms))
	for _, p := range perms {
		for _, r := range PermissionRenames {
			if p == r.From {
				p = r.To
				break
			}
		}
		if retiredPermission(p) {
			warnRetiredPermission(id, p)
			continue
		}
		// Two spellings of one grant collapse into one: the list is a
		// set of grants, and the caller renders it.
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

func retiredPermission(name string) bool {
	for _, r := range RetiredPermissions {
		if r.Name == name {
			return true
		}
	}
	return false
}

// warnedInputs dedupes the compatibility warnings across parses:
// Store.List() and the agent host re-parse manifests on every scan, and
// one stale manifest must not become a flood.
var warnedInputs sync.Map

// warnOnce logs one compatibility warning per (plugin, input) pair.
func warnOnce(key, msg string, attrs ...otellog.KeyValue) {
	if _, loaded := warnedInputs.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	telemetry.Warn(context.Background(), msg, attrs...)
}

func warnRetiredPermission(id, name string) {
	warnOnce(id+"\x00permission\x00"+name,
		"plugins: manifest declares a retired permission; ignoring it",
		otellog.String("plugin", id),
		otellog.String("permission", name),
	)
}

// warnLegacyGrantPair logs the redundant half of a manifest that writes
// one grant under both its old and its new name. The plugin keeps
// loading on the canonical name, which is what the log line has to say:
// the pair reads as a deliberate "works on both hosts" manifest, not as
// a mistake to refuse.
func warnLegacyGrantPair(id string, r PermissionRename) {
	warnOnce(id+"\x00permission-pair\x00"+r.From,
		"plugins: manifest declares a grant and its replacement spelling; "+
			"reading it as the replacement",
		otellog.String("plugin", id),
		otellog.String("declared", r.From),
		otellog.String("canonical", r.To),
	)
}

// warnLegacyKraftPair logs the redundant half of a manifest that writes
// the kraft section under both spellings. The two agree (a pair that
// disagrees is refused), so the new key is used and the old one is
// named once.
func warnLegacyKraftPair(id string) {
	warnOnce(id+"\x00kraft-section-pair",
		"plugins: manifest declares both kraft and its legacy capability "+
			"key with the same value; reading the kraft key",
		otellog.String("plugin", id),
	)
}

// legacyContributeKeys are the manifest UI-contribution keys older
// builds read. Nothing consumes them any more; the names survive so the
// breadcrumb can say which declaration is being ignored instead of
// crying wolf about a segment holding nothing.
var legacyContributeKeys = []string{"settingsPanels", "sidebarEntries", "pets"}

// legacyContributeKeysWritten returns the UI-contribution keys a
// manifest still writes with something in them. An empty object, a
// null, a list or an object without any of those keys was never read by
// anything, and warning about it would train the author to ignore the
// warning that matters.
func legacyContributeKeysWritten(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var declared map[string]json.RawMessage
	if err := json.Unmarshal(raw, &declared); err != nil {
		return nil
	}
	var out []string
	for _, key := range legacyContributeKeys {
		value, ok := declared[key]
		if !ok {
			continue
		}
		switch strings.TrimSpace(string(value)) {
		case "", "null", "[]", "{}":
			continue
		}
		out = append(out, key)
	}
	return out
}

// warnLegacyContributes logs a stale contributes segment once per
// plugin, naming the keys it still writes. Nothing validates or reports
// the segment any more, so the log line is the only breadcrumb an
// author gets for the panel that never renders: UI contributions
// register from the bundle (ctx.settingsPanels.add,
// ctx.sidebarEntries.add, ctx.pets.add).
func warnLegacyContributes(id string, keys []string) {
	warnOnce(id+"\x00contributes",
		"plugins: manifest declares contributes."+strings.Join(keys, ", contributes.")+
			", which the host no longer reads; register panels, sidebar "+
			"entries and pet packs from the bundle",
		otellog.String("plugin", id),
		otellog.String("contributes", strings.Join(keys, ",")),
	)
}
