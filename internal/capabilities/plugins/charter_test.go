package plugins

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// charterUpdate rewrites charter.md from the tables instead of checking
// it. It is the only way the generated view changes.
var charterUpdate = flag.Bool("update", false,
	"rewrite charter.md from the charter tables")

// TestCharterRowsAreWellFormed keeps the tables themselves honest. A
// row that names a consumer, a runtime or a status outside the closed
// sets, a registration row with no service, a manifest row with no
// path, or a legacy row that does not say how it leaves, makes the
// scans below unfalsifiable.
func TestCharterRowsAreWellFormed(t *testing.T) {
	consumers := map[CharterConsumer]bool{
		ConsumerUI: true, ConsumerAgent: true, ConsumerPlatform: true,
	}
	declarations := map[CharterDeclaration]bool{
		DeclaredInManifest: true, DeclaredByRegistration: true,
	}
	runtimes := map[CharterRuntime]bool{
		RuntimeWebview: true, RuntimeKraft: true,
		RuntimeSubprocess: true, RuntimeData: true,
	}
	statuses := map[CharterStatus]bool{
		StatusCurrent: true, StatusReserved: true,
	}
	surfaces := map[CharterSurface]bool{
		SurfaceWebview: true, SurfaceKraft: true,
	}

	services := map[string]string{}
	paths := map[string]string{}
	methods := map[string]string{}
	claim := func(seen map[string]string, what, id, row string) {
		if prev, ok := seen[id]; ok {
			t.Errorf("%s %q is claimed by both %s and %s", what, id, prev, row)
			return
		}
		seen[id] = row
	}

	for _, k := range ContributionKinds {
		switch {
		case k.ID == "":
			t.Error("a contribution row has no id")
			continue
		case !strings.HasPrefix(k.ID, string(k.Consumer)+"."):
			t.Errorf("%s: id does not start with its consumer", k.ID)
		case k.Summary == "":
			t.Errorf("%s: no summary", k.ID)
		case !consumers[k.Consumer]:
			t.Errorf("%s: unknown consumer %q", k.ID, k.Consumer)
		case !declarations[k.Declaration]:
			t.Errorf("%s: unknown declaration %q", k.ID, k.Declaration)
		case !runtimes[k.Runtime]:
			t.Errorf("%s: unknown runtime %q", k.ID, k.Runtime)
		case !statuses[k.Status]:
			t.Errorf("%s: unknown status %q", k.ID, k.Status)
		case strings.TrimSpace(k.Activation) == "":
			t.Errorf("%s: no activation", k.ID)
		case strings.TrimSpace(k.Teardown) == "":
			t.Errorf("%s: no teardown", k.ID)
		// A kind the manifest declares reaches the model (or the tool
		// registry) through a host-enforced limit; a row that names the
		// declaration without its bound reads as if the size were
		// unconstrained, which is the question #15 asked.
		case k.Declaration == DeclaredInManifest && len(k.ManifestPaths) > 0 &&
			strings.TrimSpace(k.Bound) == "":
			t.Errorf("%s: a manifest kind must state its per-plugin bound",
				k.ID)
		case k.Status != StatusCurrent && len(k.Note) < 40:
			t.Errorf("%s: a %s row needs a note that says why it is still "+
				"here and how it leaves", k.ID, k.Status)
		}
		if k.Grant != "" && !AllowedPermissions[k.Grant] {
			t.Errorf("%s: grant %q is not in AllowedPermissions",
				k.ID, k.Grant)
		}
		if k.Grant != "" && !strings.HasSuffix(k.Grant, ":provide") {
			t.Errorf("%s: grant %q does not spell itself kind:provide — "+
				"a plugin provides a contribution", k.ID, k.Grant)
		}
		hasAnchor := len(k.Services) > 0 || len(k.ManifestPaths) > 0
		if !hasAnchor && k.Status != StatusReserved {
			t.Errorf("%s: no service and no manifest path", k.ID)
		}
		switch k.Declaration {
		case DeclaredByRegistration:
			if len(k.ManifestPaths) > 0 {
				t.Errorf("%s: a registration row cannot declare manifest "+
					"paths (that is what shadows are for)", k.ID)
			}
		case DeclaredInManifest:
			if len(k.Services) > 0 {
				t.Errorf("%s: a manifest row cannot own a service", k.ID)
			}
			if len(k.ManifestPaths) == 0 && k.Status != StatusReserved {
				t.Errorf("%s: a manifest row needs a path", k.ID)
			}
		}
		for _, s := range k.Services {
			claim(services, "service", s, k.ID)
		}
		for _, p := range k.ManifestPaths {
			claim(paths, "manifest path", p, k.ID)
		}
	}

	// The register clock: every consumer face says what it does when
	// the registry revision moves, and every consumer a contribution
	// row uses has such a row — a new consumer cannot land without a
	// refresh rule, and the activation cells stay enforceable.
	clockFaces := map[string]bool{
		string(ConsumerUI): true, string(ConsumerAgent): true,
		"kraft": true, string(ConsumerPlatform): true,
	}
	clock := map[string]bool{}
	for _, f := range FaceRefreshes {
		switch {
		case f.Face == "":
			t.Error("a clock row has no face")
			continue
		case !clockFaces[f.Face]:
			t.Errorf("clock row %q: unknown face", f.Face)
		case clock[f.Face]:
			t.Errorf("clock row %q is listed twice", f.Face)
		case len(f.What) < 40:
			t.Errorf("clock row %q: what happens is too short to judge",
				f.Face)
		}
		clock[f.Face] = true
	}
	consumersUsed := map[CharterConsumer]bool{}
	for _, k := range ContributionKinds {
		consumersUsed[k.Consumer] = true
	}
	for c := range consumersUsed {
		if !clock[string(c)] {
			t.Errorf("consumer %q has no clock row in FaceRefreshes", c)
		}
	}

	// A row that leans on another one says so by name; a name that is
	// not a row is a dangling dependency nobody can follow.
	interfaceIDs := map[string]bool{}
	for _, in := range HostInterfaces {
		interfaceIDs[in.ID] = true
	}
	for _, in := range HostInterfaces {
		for _, dep := range in.Requires {
			if !interfaceIDs[dep] {
				t.Errorf("%s requires %q, which is not an interface row",
					in.ID, dep)
			}
		}
	}

	for _, in := range HostInterfaces {
		switch {
		case in.ID == "":
			t.Error("an interface row has no id")
			continue
		case in.Summary == "":
			t.Errorf("%s: no summary", in.ID)
		case !surfaces[in.Surface]:
			t.Errorf("%s: unknown surface %q", in.ID, in.Surface)
		case !statuses[in.Status]:
			t.Errorf("%s: unknown status %q", in.ID, in.Status)
		case strings.TrimSpace(in.Scope) == "":
			t.Errorf("%s: no scope", in.ID)
		case in.Status != StatusCurrent && len(in.Note) < 40:
			t.Errorf("%s: a %s row needs a note that says why it is still "+
				"here and how it leaves", in.ID, in.Status)
		}
		if in.Grant != "" && !AllowedPermissions[in.Grant] {
			t.Errorf("%s: grant %q is not in AllowedPermissions",
				in.ID, in.Grant)
		}
		switch in.Surface {
		case SurfaceWebview:
			if len(in.Services) == 0 {
				t.Errorf("%s: a webview row needs a service key", in.ID)
			}
			if len(in.Methods) > 0 {
				t.Errorf("%s: a webview row cannot own kraft methods", in.ID)
			}
		case SurfaceKraft:
			if len(in.Methods) == 0 {
				t.Errorf("%s: a kraft row needs method names", in.ID)
			}
			if len(in.Services) > 0 {
				t.Errorf("%s: a kraft row cannot own a service", in.ID)
			}
		}
		for _, s := range in.Services {
			claim(services, "service", s, in.ID)
		}
		for _, m := range in.Methods {
			claim(methods, "kraft method", m, in.ID)
		}
	}
}

// TestCharterSpendsEveryGrant is half of the mirror the header promises:
// no permission without a row that spends it, and no row spending a
// permission that no longer exists. A permission nothing spends has to
// be retired instead: CheckPermissions is fail-closed, but the parser
// drops a retired name before that check, so every installed manifest
// keeps loading while the vocabulary stays free of grants that buy
// nothing — that is how a rusted permission is found instead of
// remembered.
func TestCharterSpendsEveryGrant(t *testing.T) {
	spenders := map[string][]string{}
	for _, k := range ContributionKinds {
		if k.Grant != "" {
			spenders[k.Grant] = append(spenders[k.Grant], k.ID)
		}
	}
	for _, in := range HostInterfaces {
		if in.Grant != "" {
			spenders[in.Grant] = append(spenders[in.Grant], in.ID)
		}
	}

	for grant := range AllowedPermissions {
		rows := spenders[grant]
		if len(rows) > 0 {
			continue
		}
		t.Errorf("%q is in AllowedPermissions and no row spends it: "+
			"add the row that consumes it, or retire it "+
			"(RetiredPermissions, with a row in LegacyManifestInputs)",
			grant)
	}
}

// TestCharterLegacyInputsAreBackedByCode holds the disposition table
// against the vocabulary machinery: every rename and every retirement
// has a row that tells a manifest author what happens to it, and every
// row that claims to translate or ignore one says so about something
// the parser actually does. A retired name cannot creep back into
// AllowedPermissions, and an old spelling cannot be both translated
// and retired.
func TestCharterLegacyInputsAreBackedByCode(t *testing.T) {
	renames := map[string]string{}
	for _, r := range PermissionRenames {
		renames[r.From] = r.To
		if !AllowedPermissions[r.To] {
			t.Errorf("rename %q targets %q, which is not a permission",
				r.From, r.To)
		}
		if AllowedPermissions[r.From] {
			t.Errorf("rename source %q is still in AllowedPermissions",
				r.From)
		}
	}
	retired := map[string]bool{}
	for _, r := range RetiredPermissions {
		if retired[r.Name] {
			t.Errorf("%q is retired twice", r.Name)
		}
		retired[r.Name] = true
		if AllowedPermissions[r.Name] {
			t.Errorf("retired %q is still in AllowedPermissions", r.Name)
		}
		if _, ok := renames[r.Name]; ok {
			t.Errorf("%q is both renamed and retired", r.Name)
		}
		if len(r.Note) < 40 {
			t.Errorf("%q: the retirement note is too short to judge", r.Name)
		}
	}
	actions := map[LegacyAction]bool{
		LegacyTranslate: true, LegacyIgnore: true, LegacyReject: true,
	}
	rows := map[string]LegacyManifestInput{}
	for _, in := range LegacyManifestInputs {
		if !actions[in.Action] {
			t.Errorf("%q: unknown legacy action %q", in.Name, in.Action)
		}
		if in.Authoring != "" && !actions[in.Authoring] {
			t.Errorf("%q: unknown authoring action %q", in.Name, in.Authoring)
		}
		// A row that splits the two audiences has to say so: the same
		// action on both sides belongs in Action alone.
		if in.Authoring == in.Action {
			t.Errorf("%q: the authoring action repeats the installed one",
				in.Name)
		}
		if len(in.Note) < 40 {
			t.Errorf("%q: the disposition note is too short to judge", in.Name)
		}
		if in.Action == LegacyTranslate && in.Target == "" {
			t.Errorf("%q: a translate row must name its target", in.Name)
		}
		if rows[in.Name].Name != "" {
			t.Errorf("%q has two disposition rows", in.Name)
		}
		rows[in.Name] = in
		// A permission name (namespaced with ":") may only be
		// translated or ignored, and only in ways the tables agree
		// with; key spellings and rejection shapes carry their own
		// tests.
		if !strings.Contains(in.Name, ":") || in.Action == LegacyReject {
			continue
		}
		switch in.Action {
		case LegacyTranslate:
			if to, ok := renames[in.Name]; !ok || to != in.Target {
				t.Errorf("%q says translate to %q, the rename table "+
					"says %q", in.Name, in.Target, to)
			}
		case LegacyIgnore:
			if !retired[in.Name] {
				t.Errorf("%q says ignore, but it is not a retired "+
					"permission", in.Name)
			}
		}
	}
	for from := range renames {
		if rows[from].Name == "" {
			t.Errorf("rename %q has no disposition row", from)
		}
	}
	for name := range retired {
		if rows[name].Name == "" {
			t.Errorf("retired permission %q has no disposition row", name)
		}
	}
}

// TestCharterCoversEveryManifestField is the other half, and the one the
// plan asked for by name: every JSON field the Manifest struct carries
// must be a skeleton entry or an anchor of a contribution row. A new
// field — a new declaration point — cannot land without a row that says
// who consumes it.
func TestCharterCoversEveryManifestField(t *testing.T) {
	leaves := manifestLeafPaths(t)

	claimed := map[string]string{}
	for _, k := range ContributionKinds {
		for _, p := range k.ManifestPaths {
			claimed[p] = k.ID
		}
	}
	for p := range ManifestSkeleton {
		claimed[p] = "ManifestSkeleton"
	}

	covered := func(prefixes map[string]string, path string) string {
		for p := range prefixes {
			if path == p || strings.HasPrefix(path, p+".") {
				return p
			}
		}
		return ""
	}
	for _, leaf := range leaves {
		if covered(claimed, leaf) == "" {
			t.Errorf("manifest field %q is neither a skeleton entry nor an "+
				"anchor of a contribution row: say who consumes it", leaf)
		}
	}
	for _, p := range sortedKeys(claimed) {
		found := false
		for _, leaf := range leaves {
			if leaf == p || strings.HasPrefix(leaf, p+".") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s claims manifest path %q, which the Manifest "+
				"struct does not have", claimed[p], p)
		}
	}
}

// TestCharterCoversEveryFrontendService reads the frontend's plugin
// host and checks three things: the two lists the host keeps are
// internally consistent (the injectable services are exactly the
// permission-gated ones plus the always-available ones, and the type
// union matches them), and the charter claims every service the host
// actually provides, exactly once.
func TestCharterCoversEveryFrontendService(t *testing.T) {
	types := charterRead(t, "frontend/src/plugins/types.ts")
	host := charterRead(t, "frontend/src/plugins/host.ts")

	union := quotedBlock(t, types, "export type PluginServiceKey =", ";")
	provided, accessors := providedServices(t, host)
	always := quotedBlock(t, host, "const ALWAYS_SERVICES", "];")
	gated := gatedServices(t, host)

	for s := range union {
		if !provided[s] {
			t.Errorf("PluginServiceKey names %q, which the host does not "+
				"provide", s)
		}
	}
	for s := range provided {
		if !union[s] {
			t.Errorf("the host provides %q, which PluginServiceKey does "+
				"not name", s)
		}
	}
	want := map[string]bool{}
	for s := range always {
		want[s] = true
	}
	for s := range gated {
		want[s] = true
	}
	for s := range want {
		if !provided[s] {
			t.Errorf("ALWAYS_SERVICES/PERMISSION_GATED names %q, which the "+
				"host does not provide", s)
		}
	}
	for s := range provided {
		if !want[s] {
			t.Errorf("the host provides %q but lists it in neither "+
				"ALWAYS_SERVICES nor PERMISSION_GATED", s)
		}
	}

	claimants := map[string]string{}
	for _, k := range ContributionKinds {
		for _, s := range k.Services {
			claimants[s] = k.ID
		}
	}
	for _, in := range HostInterfaces {
		for _, s := range in.Services {
			claimants[s] = in.ID
		}
	}
	all := map[string]bool{}
	for s := range provided {
		all[s] = true
	}
	for s := range accessors {
		all[s] = true
	}
	for s := range all {
		if claimants[s] == "" {
			t.Errorf("the host provides %q and no charter row claims it", s)
		}
	}
	for s, row := range claimants {
		if !all[s] {
			t.Errorf("%s claims service %q, which the host does not "+
				"provide", row, s)
		}
	}

	// A service the frontend isolates unless the permission is declared
	// must be the same permission the charter's row names, in both
	// directions: the isolation is enforcement, and the table has to
	// describe it.
	rowGrant := map[string]string{}
	for _, k := range ContributionKinds {
		for _, s := range k.Services {
			rowGrant[s] = k.Grant
		}
	}
	for _, in := range HostInterfaces {
		for _, s := range in.Services {
			rowGrant[s] = in.Grant
		}
	}
	for s, grant := range gated {
		if rowGrant[s] != grant {
			t.Errorf("the frontend gates %q on %q, the charter says %q",
				s, grant, rowGrant[s])
		}
	}
	for s, grant := range rowGrant {
		if grant == "" {
			continue
		}
		if gated[s] != grant {
			t.Errorf("the charter gates %q on %q, the frontend does not "+
				"(PERMISSION_GATED has %q)", s, grant, gated[s])
		}
	}
}

// TestCharterCoversEveryKraftPrimitive reads the dispatch in
// kraft.go's handlePrimitive and checks it against the interface
// table's kraft rows, in both directions.
func TestCharterCoversEveryKraftPrimitive(t *testing.T) {
	src := charterRead(t, "internal/capabilities/plugins/kraft/kraft.go")
	const anchor = "func (m *Manager) handlePrimitive("
	start := strings.Index(src, anchor)
	if start < 0 {
		t.Fatalf("kraft.go: handlePrimitive is gone; the charter scan needs " +
			"a new anchor")
	}
	rest := src[start:]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		t.Fatal("kraft.go: handlePrimitive has no successor function any more")
	}
	methodRe := regexp.MustCompile(`"([a-z._]+)"`)
	scanned := map[string]bool{}
	for _, line := range strings.Split(rest[:end], "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "case ") {
			continue
		}
		for _, m := range methodRe.FindAllStringSubmatch(line, -1) {
			scanned[m[1]] = true
		}
	}
	if len(scanned) == 0 {
		t.Fatal("kraft.go: the primitive dispatch has no case labels any more")
	}

	claimed := map[string]string{}
	for _, in := range HostInterfaces {
		for _, m := range in.Methods {
			claimed[m] = in.ID
		}
	}
	for m := range scanned {
		if claimed[m] == "" {
			t.Errorf("kraft primitive %q is dispatched and no charter row "+
				"claims it", m)
		}
	}
	for m, row := range claimed {
		if !scanned[m] {
			t.Errorf("%s claims kraft primitive %q, which the dispatch "+
				"does not handle", row, m)
		}
	}
}

// goFuncBody returns the declaration of the named Go function in src,
// signature and body, ending before the next top-level func. The name is
// matched as the identifier after the receiver, so callers pass
// "handleSecret" or "handlePluginSessionImport" without the receiver.
// An empty result means the function is not in this file, which the
// callers report as a stale row.
func goFuncBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, ") "+name+"(")
	if start < 0 {
		return ""
	}
	open := strings.LastIndex(src[:start], "\nfunc ")
	if open < 0 {
		return ""
	}
	rest := src[open+1:]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestCharterGrantsAreSpent is the scan the secret.* row's near-miss
// asked for. The primitive scan reads the dispatch's case labels, so a
// row's grant could be wrong in either direction while the suite stayed
// green — the kraft secret.* primitives claimed a secrets:auth gate that
// nothing checked for as long as they existed. Every row that declares a
// grant names where it is spent, every named check has to exist and
// check exactly that grant, and every requirePermission call in the
// kraft runtime has to belong to a row.
func TestCharterGrantsAreSpent(t *testing.T) {
	src := charterRead(t, "internal/capabilities/plugins/kraft/kraft.go")
	funcNameRe := regexp.MustCompile(`^\([^)]*\) (\w+)\(`)
	grantRe := regexp.MustCompile(`requirePermission\(p\.id, "([^"]+)"\)`)
	sites := map[string]string{}
	for _, chunk := range strings.Split(src, "\nfunc ") {
		m := funcNameRe.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}
		for _, g := range grantRe.FindAllStringSubmatch(chunk, -1) {
			sites[m[1]] = g[1]
		}
	}
	if len(sites) == 0 {
		t.Fatal("kraft.go: no permission check found any more; the scan " +
			"needs a new anchor")
	}

	claimed := map[string]bool{}
	for _, in := range HostInterfaces {
		if in.Surface != SurfaceKraft || in.Grant == "" {
			continue
		}
		if len(in.Checks) == 0 {
			t.Errorf("%s declares grant %q and names no check: say where "+
				"the grant is spent", in.ID, in.Grant)
			continue
		}
		for _, check := range in.Checks {
			switch {
			case check.Empty():
				t.Errorf("%s: a check names neither a handler nor a host func",
					in.ID)
			case check.Handler != "" && check.Func != "":
				t.Errorf("%s: %s is both a kraft handler and a host func",
					in.ID, check.Describe())
			case check.Handler != "":
				got, ok := sites[check.Handler]
				if !ok {
					t.Errorf("%s: kraft.go:%s does not call "+
						"requirePermission", in.ID, check.Handler)
					continue
				}
				if got != in.Grant {
					t.Errorf("%s: kraft.go:%s checks %q, the row declares "+
						"%q", in.ID, check.Handler, got, in.Grant)
				}
				claimed[check.Handler] = true
			default:
				if check.File == "" {
					t.Errorf("%s: a host check names no file", in.ID)
					continue
				}
				body := goFuncBody(t, charterRead(t, check.File), check.Func)
				if body == "" {
					t.Errorf("%s: %s has no function %s",
						in.ID, check.File, check.Func)
					continue
				}
				want := `pluginHasPermission(pluginID, "` + in.Grant + `")`
				if !strings.Contains(body, want) {
					t.Errorf("%s: %s does not check %q",
						in.ID, check.Func, in.Grant)
				}
			}
		}
	}

	// The other direction: a grant spent inside the kraft runtime that
	// no row admits to is a gate the table does not explain.
	for fn, grant := range sites {
		if !claimed[fn] {
			t.Errorf("kraft.go:%s checks %q and no charter row claims it",
				fn, grant)
		}
	}
}

// TestCharterDocument is the generated view: charter.md beside this
// file. It is checked in because a reviewer reads a diff; the test is
// what keeps it from drifting.
func TestCharterDocument(t *testing.T) {
	path := filepath.Join(charterDir(t), "charter.md")
	want := renderCharter()
	if *charterUpdate {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", path)
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/capabilities/plugins "+
			"-run TestCharterDocument -update)", err)
	}
	if string(got) != want {
		t.Errorf("charter.md is stale: run go test " +
			"./internal/capabilities/plugins -run TestCharterDocument -update")
	}
}

// ---- scanning helpers ----

// manifestLeafPaths walks the Manifest struct and returns every JSON
// path whose value is a leaf: strings, numbers, bools, raw JSON, or
// slices of them. Object and array-of-object fields are walked through.
func manifestLeafPaths(t *testing.T) []string {
	t.Helper()
	var out []string
	var walk func(prefix string, tp reflect.Type)
	walk = func(prefix string, tp reflect.Type) {
		for tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Slice {
			tp = tp.Elem()
		}
		if tp.Kind() != reflect.Struct {
			out = append(out, prefix)
			return
		}
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Fatalf("Manifest.%s has no JSON name: the charter cannot "+
					"cover a field it cannot see", f.Name)
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			walk(path, f.Type)
		}
	}
	walk("", reflect.TypeOf(Manifest{}))
	sort.Strings(out)
	return out
}

// providedServices returns the Cordis services host.ts provides and the
// accessors it exposes. The two are separate because only the former
// may be named in inject (the type union is checked against it).
func providedServices(t *testing.T, host string) (map[string]bool, map[string]bool) {
	t.Helper()
	re := regexp.MustCompile(`ctx\.(provide|accessor)\(\s*'([a-zA-Z0-9_]+)'`)
	provided := map[string]bool{}
	accessors := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(host, -1) {
		if m[1] == "provide" {
			provided[m[2]] = true
			continue
		}
		accessors[m[2]] = true
	}
	if len(provided) == 0 || len(accessors) == 0 {
		t.Fatal("host.ts: found no ctx.provide/ctx.accessor calls; the " +
			"charter scan needs new anchors")
	}
	return provided, accessors
}

// gatedServices reads PERMISSION_GATED: the services host.ts isolates
// from a plugin branch unless the manifest declares the permission.
func gatedServices(t *testing.T, host string) map[string]string {
	t.Helper()
	start := strings.Index(host, "const PERMISSION_GATED")
	if start < 0 {
		t.Fatal("host.ts: PERMISSION_GATED is gone; the charter scan needs " +
			"a new anchor")
	}
	rest := host[start:]
	end := strings.Index(rest, "\n};")
	if end < 0 {
		t.Fatal("host.ts: PERMISSION_GATED has no closing brace")
	}
	out := map[string]string{}
	re := regexp.MustCompile(`(\w+):\s*'([^']+)'`)
	for _, m := range re.FindAllStringSubmatch(rest[:end], -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatal("host.ts: PERMISSION_GATED is empty; the charter scan needs " +
			"a new anchor")
	}
	return out
}

// quotedBlock returns the single-quoted names between a marker and the
// first closing delimiter after it.
func quotedBlock(t *testing.T, src, marker, end string) map[string]bool {
	t.Helper()
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("anchor %q is gone: the charter scan needs a new one",
			marker)
	}
	rest := src[start+len(marker):]
	stop := strings.Index(rest, end)
	if stop < 0 {
		t.Fatalf("anchor %q has no closing %q", marker, end)
	}
	out := map[string]bool{}
	re := regexp.MustCompile(`'([a-zA-Z0-9_]+)'`)
	for _, m := range re.FindAllStringSubmatch(rest[:stop], -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatalf("anchor %q lists no names", marker)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// charterDir is this package's directory, found through the test file
// rather than the working directory.
func charterDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Dir(file)
}

// charterRead reads a repo-relative file, so the scan does not depend on
// the working directory.
func charterRead(t *testing.T, rel string) string {
	t.Helper()
	dir := charterDir(t)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", charterDir(t))
		}
		dir = parent
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// ---- rendering ----

// renderCharter is the whole generated view of the tables. It is
// deliberately plain: one table per question, a detail list per row, and
// the grants and manifest dispositions spelled out, so a reviewer can
// read the framework in one file.
func renderCharter() string {
	var b strings.Builder
	b.WriteString("# The plugin charter\n\n")
	b.WriteString("<!-- Generated from charter.go by TestCharterDocument. Do not edit " +
		"by hand:\n")
	b.WriteString("     go test ./internal/capabilities/plugins -run " +
		"TestCharterDocument -update -->\n\n")
	b.WriteString("A plugin has two halves: the bundle the shell loads into a webview, " +
		"and the\nmachine half the host runs for it. Everything a plugin can " +
		"**give** the host or\n**call** on it answers the same five questions, so the " +
		"next contribution kind is\na row in one of the two tables below rather than a " +
		"new mechanism:\n\n")
	b.WriteString("| question | column |\n| --- | --- |\n")
	b.WriteString("| Who consumes it? | consumer |\n")
	b.WriteString("| Where does its code run? | runtime |\n")
	b.WriteString("| How does the host learn it exists? | declared by |\n")
	b.WriteString("| What authorizes it? | grant |\n")
	b.WriteString("| When does it live, and what removes it? | activation / teardown |\n\n")
	b.WriteString("The declaration line is drawn per consumer, not per taste. The agent " +
		"runtime\n(and, later, the app platform) must enumerate a plugin's half " +
		"*without executing\nit*, so their contributions are declared in " +
		"`plugin.json`; a UI contribution *is\ncode*, so its only source is the " +
		"registration the bundle performs while it\nruns. The manifest carried a " +
		"copy of the UI half once —\n`contributes.settingsPanels`, " +
		"`contributes.sidebarEntries`, `contributes.pets` — parsed and\n" +
		"rendered by nothing; the cleanup deleted the segments, and a manifest " +
		"that\nstill writes one keeps loading.\n\n")
	b.WriteString("`charter_test.go` enforces the tables in both directions: every " +
		"permission,\nmanifest field, service and kraft primitive the code has must " +
		"be claimed by a row,\nand every row must name something that exists. " +
		"`legacy` and `reserved` rows\ncarry the note that says what has to change " +
		"for them to leave.\n\n")

	b.WriteString("## Contributions — what a plugin gives\n\n")
	kindRows := make([][]string, 0, len(ContributionKinds))
	for _, k := range ContributionKinds {
		kindRows = append(kindRows, []string{
			"`" + k.ID + "`", string(k.Consumer), string(k.Declaration),
			string(k.Runtime), grantCell(k.Grant), anchorCell(k),
			boundCell(k.Bound),
		})
	}
	b.WriteString(mdTable([]string{
		"kind", "consumer", "declared by", "runs in", "grant", "anchor",
		"bound (per plugin)",
	}, kindRows))
	b.WriteString("\n")
	b.WriteString("Most bounds are per plugin. The agent source adds one " +
		"aggregate cap\nabove them — the whole registry is bounded at 256 " +
		"tools and 512 KiB of\ndefinitions, with one warning naming what " +
		"was dropped — because the number\nof installed plugins is not " +
		"bounded anywhere. What the model actually\nsees per round is the " +
		"deployment's budget: opencraft's `tools.yaml` pins the\nvisible " +
		"set to 64 definitions / 48 KiB and the discovery pool to 48 / " +
		"32 KiB,\nand core skips an oversized definition rather than " +
		"starving the smaller ones\nbehind it. A registry of many plugins " +
		"therefore costs `tool_search` results,\nnot context — and the " +
		"aggregate cap is what keeps even those finite.\n\n")
	for _, k := range ContributionKinds {
		fmt.Fprintf(&b, "- **`%s`** — %s\n", k.ID, k.Summary)
		fmt.Fprintf(&b, "  - comes alive when: %s\n", k.Activation)
		fmt.Fprintf(&b, "  - removed by: %s\n", k.Teardown)
		if k.Bound != "" {
			fmt.Fprintf(&b, "  - bound: %s\n", k.Bound)
		}
		if status := k.Status; status != StatusCurrent {
			fmt.Fprintf(&b, "  - status: %s\n", status)
		}
		if k.Note != "" {
			fmt.Fprintf(&b, "  - note: %s\n", k.Note)
		}
	}
	b.WriteString("\n")

	b.WriteString("## Host interfaces — what a plugin may call\n\n")
	ifRows := make([][]string, 0, len(HostInterfaces))
	for _, in := range HostInterfaces {
		ifRows = append(ifRows, []string{
			"`" + in.ID + "`", string(in.Surface), anchorCell(in),
			grantCell(in.Grant), in.Scope,
		})
	}
	b.WriteString(mdTable([]string{
		"interface", "surface", "anchor", "grant", "scope",
	}, ifRows))
	b.WriteString("\n")
	for _, in := range HostInterfaces {
		fmt.Fprintf(&b, "- **`%s`** — %s\n", in.ID, in.Summary)
		if in.Status != StatusCurrent {
			fmt.Fprintf(&b, "  - status: %s\n", in.Status)
		}
		if len(in.Checks) > 0 {
			var parts []string
			for _, check := range in.Checks {
				parts = append(parts, check.Describe())
			}
			fmt.Fprintf(&b, "  - checked by: %s\n", strings.Join(parts, ", "))
		}
		if len(in.Requires) > 0 {
			fmt.Fprintf(&b, "  - needs too: %s\n", strings.Join(in.Requires, ", "))
		}
		if in.Note != "" {
			fmt.Fprintf(&b, "  - note: %s\n", in.Note)
		}
	}
	b.WriteString("\n")

	b.WriteString("## The register clock\n\n")
	b.WriteString("Every successful registry mutation moves the store's revision " +
		"(`plugins.Store.Revision`).\nThe contributions above say when a " +
		"contribution comes alive; this is what each face\ndoes when it sees " +
		"the revision behind the one it last read from.\n\n")
	clockRows := make([][]string, 0, len(FaceRefreshes))
	for _, f := range FaceRefreshes {
		clockRows = append(clockRows, []string{"`" + f.Face + "`", f.What})
	}
	b.WriteString(mdTable([]string{
		"face", "what it does when the revision moves",
	}, clockRows))
	b.WriteString("\n")

	b.WriteString("## Grants\n\n")
	b.WriteString("Every permission in `plugins.AllowedPermissions` is spent by a " +
		"row above.\nContribution grants are spelled `kind:provide` — a plugin " +
		"provides a\ncontribution. `CheckPermissions` is fail-closed, so a name " +
		"leaves the set only\nby retirement (`RetiredPermissions`): the parser " +
		"drops it on the way in and\nlogs once, which keeps every " +
		"already-installed manifest loading.\n\n")
	spenders := map[string][]string{}
	var grantOrder []string
	addSpender := func(grant, row string) {
		if _, ok := spenders[grant]; !ok {
			grantOrder = append(grantOrder, grant)
		}
		spenders[grant] = append(spenders[grant], "`"+row+"`")
	}
	for _, k := range ContributionKinds {
		if k.Grant != "" {
			addSpender(k.Grant, k.ID)
		}
	}
	for _, in := range HostInterfaces {
		if in.Grant != "" {
			addSpender(in.Grant, in.ID)
		}
	}
	for _, grant := range grantOrder {
		fmt.Fprintf(&b, "- `%s` — spent by %s\n",
			grant, strings.Join(spenders[grant], ", "))
	}
	b.WriteString("\nRetired and ignored — a manifest that still declares one " +
		"loads, the name is\ndropped and logged once:\n\n")
	for _, r := range RetiredPermissions {
		fmt.Fprintf(&b, "- `%s` — %s\n", r.Name, r.Note)
	}
	b.WriteString("\n")

	b.WriteString("## Legacy manifest inputs\n\n")
	b.WriteString("Older spellings a manifest may still carry, and what the host " +
		"does with each:\n`translate` loads under the new name, `ignore` accepts " +
		"the manifest and drops\nthe name, `reject` refuses two spellings where one " +
		"belongs. The two columns\nsplit by audience: `installed` is a plugin the " +
		"registry is serving, `offered`\nis a source handed to Inspect, Install, " +
		"Update or Rollback.\n\n")
	legacyRows := make([][]string, 0, len(LegacyManifestInputs))
	for _, in := range LegacyManifestInputs {
		legacyRows = append(legacyRows, []string{
			"`" + in.Name + "`", string(in.Action), string(in.AtAuthoring()),
			grantCell(in.Target), in.Note,
		})
	}
	b.WriteString(mdTable(
		[]string{"input", "installed", "offered", "becomes", "why"}, legacyRows))
	b.WriteString("\n")

	b.WriteString("## Manifest fields\n\n")
	b.WriteString("Every JSON path the `Manifest` struct carries. A **contribution** " +
		"path is an\nanchor of the row named; a **skeleton** path is identity or " +
		"runtime plumbing and\nis listed here with what consumes it.\n\n")
	type disposition struct{ path, kind, owner string }
	var table []disposition
	for _, k := range ContributionKinds {
		for _, p := range k.ManifestPaths {
			table = append(table, disposition{p, "contribution", "`" + k.ID + "`"})
		}
	}
	for p, why := range ManifestSkeleton {
		table = append(table, disposition{p, "skeleton", why})
	}
	sort.Slice(table, func(i, j int) bool { return table[i].path < table[j].path })
	rows := make([][]string, 0, len(table))
	for _, d := range table {
		rows = append(rows, []string{"`" + d.path + "`", d.kind, d.owner})
	}
	b.WriteString(mdTable([]string{"path", "kind", "owner"}, rows))
	b.WriteString("\n")
	return b.String()
}

func grantCell(grant string) string {
	if grant == "" {
		return "—"
	}
	return "`" + grant + "`"
}

func boundCell(bound string) string {
	if bound == "" {
		return "—"
	}
	return bound
}

func anchorCell(v any) string {
	switch row := v.(type) {
	case ContributionKind:
		var parts []string
		for _, s := range row.Services {
			parts = append(parts, "`ctx."+s+"`")
		}
		for _, p := range row.ManifestPaths {
			parts = append(parts, "`"+p+"`")
		}
		if row.Status == StatusReserved && len(parts) == 0 {
			return "—"
		}
		return strings.Join(parts, ", ")
	case HostInterface:
		var parts []string
		for _, s := range row.Services {
			parts = append(parts, "`ctx."+s+"`")
		}
		for _, m := range row.Methods {
			parts = append(parts, "`"+m+"`")
		}
		return strings.Join(parts, ", ")
	}
	return "—"
}

// mdTable renders a padded markdown table. Cells must not contain
// unescaped pipes; the charter's values do not.
func mdTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("|")
		for i, cell := range cells {
			b.WriteString(" " + cell +
				strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)) + " |")
		}
		b.WriteString("\n")
	}
	writeRow(headers)
	b.WriteString("|")
	for _, w := range widths {
		b.WriteString(strings.Repeat("-", w+2) + "|")
	}
	b.WriteString("\n")
	for _, row := range rows {
		writeRow(row)
	}
	return b.String()
}
