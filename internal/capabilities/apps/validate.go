package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/utils"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// The document-level preflight (the app platform plan, §2.8): everything
// the host can decide about an application without assembling one.
//
// It runs at two moments and must answer the same way both times —
// before an install copies a single file, and again when an enabled
// application is about to be assembled (its files may have changed under
// the registry in the meantime). That is the reason it is a function
// over an App value rather than part of Install: "valid where it came
// from" and "valid where it lives now" are the same question.
//
// Two passes, because they read different documents. The declaration
// pass reads each application layer as written, before any merge: only
// there can it tell which layer declared a reserved key or a restricted
// kind, and a merge would hide that behind the winner. The document pass
// reads the merge of the contract layer and those layers: the entry
// agent, its graph, the dependency targets, and every {file:} reference,
// with the content root they have to stay inside.

const (
	// maxScannedRefs bounds how many referenced documents the scan
	// follows: a document chain is a few files, and a package that
	// chains hundreds is not one this host should walk.
	maxScannedRefs = 256
	// maxScannedBytes caps one followed document. Referenced documents
	// are configuration (graphs, layers); payload a host would never
	// parse is skipped by extension before it gets here.
	maxScannedBytes = 8 << 20
)

// Refusal is one reason an application's material was refused, written
// for the person who has to fix the YAML: which layer, which key, and
// what the host would have done with it.
type Refusal struct {
	// Key is the thing that was refused: a resource or agent key
	// ("resources.ws", "agents.app.commit"), a manifest field
	// ("ui.entry"), a referenced path, or a layer name.
	Key string
	// Layer is the file the refusal belongs to: a layer name, the
	// manifest, or empty when neither declared it (the merge itself, or
	// a key the contract layer owns).
	Layer string
	// Reason says what the host refuses and why, in one sentence.
	Reason string
}

// String renders one refusal as a single line.
func (r Refusal) String() string {
	switch {
	case r.Layer != "" && r.Key != "":
		return fmt.Sprintf("%s: %s: %s", r.Layer, r.Key, r.Reason)
	case r.Layer != "":
		return fmt.Sprintf("%s: %s", r.Layer, r.Reason)
	case r.Key != "":
		return fmt.Sprintf("%s: %s", r.Key, r.Reason)
	default:
		return r.Reason
	}
}

// listed reports whether the manifest names this agent among the ones it
// runs. The entry is not in that list (the manifest names it in the
// agent field), which is why the check is separate from the slot rule.
func (p *preflight) listed(name string) bool {
	for _, candidate := range p.app.Agents {
		if candidate == name {
			return true
		}
	}
	return false
}

// listedAgents renders the manifest's agent list for a refusal: a
// message that says an agent is not named has to say what is.
func listedAgents(app App) string {
	if len(app.Agents) == 0 {
		return "no other agents"
	}
	quoted := make([]string, len(app.Agents))
	for i, name := range app.Agents {
		quoted[i] = strconv.Quote(name)
	}
	return strings.Join(quoted, ", ")
}

// declaredAgents renders the agents a merged document declares, for a
// refusal about a name none of them answers to.
func declaredAgents(merged deploy.Document) string {
	names := make([]string, 0, len(merged.Agents))
	for name := range merged.Agents {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "none"
	}
	sort.Strings(names)
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	return strings.Join(quoted, ", ")
}

// Refusals is the preflight's verdict: the application it refused and
// every reason, not just the first. A refused application usually has
// several things to fix — a document imported from a scenario declares
// its own bus, its own providers and its own tools — and making the
// author re-run an import once per problem is how a wizard turns into a
// debugging session.
//
// It is a value, not just an error string: the import wizard lists List,
// one row per refusal, and the text below is the same list for a caller
// that only has the error.
type Refusals struct {
	// App is the application id the refusals belong to.
	App string
	// List is every refusal, in the order the passes found them.
	List []Refusal
}

// Error renders the refusals one per line, so the text the user copies
// is the text they can act on.
func (rs Refusals) Error() string {
	switch len(rs.List) {
	case 0:
		return "apps: refused"
	case 1:
		return "apps: " + rs.List[0].String()
	}
	var b strings.Builder
	if rs.App != "" {
		fmt.Fprintf(&b, "apps: %q refused for %d reasons:", rs.App, len(rs.List))
	} else {
		fmt.Fprintf(&b, "apps: refused for %d reasons:", len(rs.List))
	}
	for _, r := range rs.List {
		b.WriteString("\n  - ")
		b.WriteString(r.String())
	}
	return b.String()
}

// Validate runs the document-level preflight over one application and
// returns a Refusals error naming every problem, or nil.
//
// It never touches the state root and never writes: it reads the content
// root the layers and their references must stay inside, the contract
// layer the application merges into, and the layers the manifest lists.
func Validate(ctx context.Context, app App) error {
	p := &preflight{
		app:          app,
		refusals:     Refusals{App: app.ID},
		seen:         make(map[string]bool),
		root:         filepath.Clean(app.ContentDir),
		layersUsable: true,
	}
	refusals, err := p.run(ctx)
	if err != nil {
		return err
	}
	if len(refusals.List) > 0 {
		return refusals
	}
	return nil
}

// layerDoc is one application layer after its own strict decode: what it
// declared, before any merge.
type layerDoc struct {
	name string
	doc  deploy.Document
}

// depRef is one dependency target waiting for the document pass, where
// the merged document is known.
type depRef struct {
	layer string
	where string
	ref   string
}

// refSite is one place a path was referenced from: the layer it belongs
// to, the position inside the document (a manifest field, a settings
// key, or a path inside a document that was itself followed), and the
// reference as it was written.
type refSite struct {
	layer string
	where string
	ref   string
}

// describe states one problem with this site, naming the reference and
// where it was found — unless the site is the reference itself (a
// manifest file), which needs no repetition.
func (r refSite) describe(reason string) string {
	if r.where == "" || r.where == strings.TrimSpace(r.ref) {
		return reason
	}
	return fmt.Sprintf("%s: %s", r.ref, reason)
}

// preflight is the state one validation run carries: what it accumulated
// and which referenced documents it has already followed.
type preflight struct {
	app  App
	root string

	contract   deploy.Document
	provenance deploy.Provenance

	refusals Refusals
	deps     []depRef
	seen     map[string]bool
	scanned  int
	// layersUsable is false once a layer file the manifest named turned
	// out to be missing or not a plain file. That is the verdict then:
	// the manifest and the package disagree about what the package is,
	// and checking the layers that are left would describe a package
	// nobody wrote — refusals an author would read while editing the
	// files around the missing one.
	layersUsable bool
}

// run executes both passes and returns everything they refused. A
// non-nil error is a host-side failure (the embedded contract layer does
// not load), never the application's fault.
func (p *preflight) run(ctx context.Context) (Refusals, error) {
	if strings.TrimSpace(p.app.ContentDir) == "" {
		return Refusals{}, errors.New("apps: content dir is required")
	}
	if len(p.app.Layers) == 0 {
		return Refusals{}, errors.New("apps: layers are required")
	}
	p.checkManifestFiles()
	if !p.layersUsable {
		return p.refusals, nil
	}
	contract, _, err := deploy.LoadLayers(ctx, []deploy.Layer{config.AppContractLayer()})
	if err != nil {
		return Refusals{}, fmt.Errorf("apps: load the contract layer: %w", err)
	}
	p.contract = contract

	layers := p.loadLayers(ctx)
	p.declarations(layers)
	if len(layers) != len(p.app.Layers) {
		// A layer that does not parse is already refused by name;
		// merging the rest would be a different document than the one
		// the runtime assembles.
		return p.refusals, nil
	}

	merged, provenance, err := p.merge(ctx, layers)
	if err != nil {
		// The merge error already names the layer that broke it (the
		// core layer loader spells out priority and name), so it is
		// appended as it reads and the document pass is skipped: there
		// is no merged document to check the rest against.
		p.refuse("", "document", err.Error())
		return p.refusals, nil
	}
	p.provenance = provenance
	p.document(merged)
	return p.refusals, nil
}

// checkManifestFiles checks what the manifest promised about the content
// root: the layers, the icon and the frontend bundle are files inside
// it. Nothing here needs the document, so it runs first: a package
// missing its layer never reaches the merge.
func (p *preflight) checkManifestFiles() {
	for _, name := range p.app.Layers {
		site := refSite{layer: ManifestFile, where: name, ref: name}
		if full, ok := p.inside(site); ok && !p.regular(site, full, "layer") {
			p.layersUsable = false
		}
	}
	ui := p.app.UI
	if ui.Entry != "" {
		p.bundleFile("ui.entry", "the frontend entry", ui.Entry, ".js")
	}
	if ui.Style != "" {
		p.bundleFile("ui.style", "the frontend stylesheet", ui.Style, ".css")
	}
	if icon := strings.TrimSpace(p.app.Icon); iconIsPath(icon) {
		site := refSite{layer: ManifestFile, where: icon, ref: icon}
		if full, ok := p.inside(site); ok {
			p.regular(site, full, "icon")
		}
	}
}

// bundleFile checks one frontend bundle file exists where the manifest
// says it does, with the extension the host will load it as.
func (p *preflight) bundleFile(key, what, rel, wantExt string) {
	if ext := strings.ToLower(filepath.Ext(rel)); ext != wantExt {
		p.refuse(ManifestFile, key, fmt.Sprintf(
			"%q must be a %s file, not %q", rel, wantExt, ext))
		return
	}
	site := refSite{layer: ManifestFile, where: rel, ref: rel}
	if full, ok := p.inside(site); ok {
		p.regular(site, full, strings.TrimPrefix(what, "the "))
	}
}

// inside resolves one relative reference against the content root and
// reports whether it is spellable at all: relative and inside the root,
// judged on the name alone. It records a refusal when it is not, and
// returns ok=false so the caller stops asking about a path it must not
// touch.
func (p *preflight) inside(site refSite) (string, bool) {
	trimmed := strings.TrimSpace(site.ref)
	switch {
	case trimmed == "":
		p.refuse(site.layer, site.where, site.describe("the path is empty"))
		return "", false
	case len(trimmed) > maxPathLen:
		p.refuse(site.layer, site.where, site.describe(fmt.Sprintf(
			"the path exceeds %d bytes", maxPathLen)))
		return "", false
	case filepath.IsAbs(trimmed) || !pathsafe.RelRef(trimmed):
		p.refuse(site.layer, site.where,
			site.describe("the path must be relative to the content root"))
		return "", false
	}
	full, err := pathsafe.ResolveUnder(p.root, trimmed)
	if err != nil {
		p.refuse(site.layer, site.where,
			site.describe("the path escapes the content root"))
		return "", false
	}
	return full, true
}

// straight checks that a resolved path stays inside the content root
// after symbolic links on the way to it are resolved. A path that names
// a link itself is caught by regular; this catches the directory that
// leads out of the package.
func (p *preflight) straight(site refSite, full string) bool {
	if pathsafe.RealWithin(p.root, full) {
		return true
	}
	p.refuse(site.layer, site.where, site.describe(
		"the path resolves outside the content root (a symbolic link points out of it)"))
	return false
}

// regular checks that a resolved path is an existing regular file. A
// missing file, a directory, a symbolic link and an unreadable entry are
// four different sentences, because they are four different mistakes.
func (p *preflight) regular(site refSite, full, what string) bool {
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.refuse(site.layer, site.where, site.describe(fmt.Sprintf(
			"the %s does not exist inside the content root", what)))
		return false
	case err != nil:
		p.refuse(site.layer, site.where, site.describe(fmt.Sprintf(
			"the %s cannot be read: %v", what, err)))
		return false
	case info.IsDir():
		p.refuse(site.layer, site.where, site.describe(fmt.Sprintf(
			"the %s is a directory", what)))
		return false
	case info.Mode()&os.ModeSymlink != 0:
		p.refuse(site.layer, site.where, site.describe(fmt.Sprintf(
			"the %s is a symbolic link; an application's content is plain files", what)))
		return false
	case !info.Mode().IsRegular():
		p.refuse(site.layer, site.where, site.describe(fmt.Sprintf(
			"the %s is not a regular file (%s)", what, info.Mode().Type())))
		return false
	}
	return true
}

// loadLayers reads every application layer and decodes it on its own: a
// partial document, strictly decoded, not yet merged with anything. The
// layer file itself is read through the core loader, so a layer source
// that escapes the content root fails here the same way it fails at
// assembly.
func (p *preflight) loadLayers(ctx context.Context) []layerDoc {
	loader := resource.NewLoader(resource.WithBaseDir(p.root))
	out := make([]layerDoc, 0, len(p.app.Layers))
	for _, name := range p.app.Layers {
		raw, err := loader.Load(ctx, resource.Source{File: name})
		if err != nil {
			p.refuse("", name, "the layer cannot be read: "+err.Error())
			continue
		}
		doc, err := utils.Decode[deploy.Document](raw)
		if err != nil {
			p.refuse("", name, "the layer is not a deployment document: "+err.Error())
			continue
		}
		out = append(out, layerDoc{name: name, doc: doc})
	}
	return out
}

// merge loads the contract layer and the application layers the way the
// assembly does (deploy.LoadLayers: ascending priority, deep merge) so
// the document pass sees the document the runtime would see — minus the
// inference overlay, which arrives from the user's settings and is not
// the application's to declare.
func (p *preflight) merge(
	ctx context.Context, layers []layerDoc,
) (deploy.Document, deploy.Provenance, error) {
	stack := []deploy.Layer{config.AppContractLayer()}
	for index, layer := range layers {
		stack = append(stack, deploy.Layer{
			Priority: config.AppLayerPriorityBase + index,
			Name:     layer.name,
			Source:   resource.Source{File: layer.name},
			BaseDir:  p.root,
		})
	}
	return deploy.LoadLayers(ctx, stack)
}

// declarations is the first pass: what each application layer declares,
// read off the layer itself. The reserved keys, the inference wiring and
// the kind table are all decided here, with the layer name in the
// refusal.
func (p *preflight) declarations(layers []layerDoc) {
	reserved := make(map[string]bool, len(p.contract.Resources))
	for key := range p.contract.Resources {
		reserved[key] = true
	}
	for _, layer := range layers {
		if version := strings.TrimSpace(layer.doc.Version); version != "" &&
			version != p.contract.Version {
			p.refuse(layer.name, "version", fmt.Sprintf(
				"an application layer cannot re-version the document (the contract layer is %q)",
				p.contract.Version))
		}
		if layer.doc.Runtime != nil {
			p.refuse(layer.name, "runtime",
				"the runtime section belongs to the contract layer (event bus, checkpoint store, external deps)")
		}
		for _, key := range sortedResourceKeys(layer.doc.Resources) {
			p.resourceKey(layer.name, reserved, key, string(layer.doc.Resources[key].Kind))
		}
		for _, name := range sortedAgentNames(layer.doc.Agents) {
			p.agentKey(layer.name, name, layer.doc.Agents[name])
		}
	}
}

// resourceKey decides one resource key an application layer declared.
func (p *preflight) resourceKey(layer string, reserved map[string]bool, key, kind string) {
	where := "resources." + key
	if reserved[key] {
		p.refuse(layer, where, fmt.Sprintf(
			"the host provides %q in the contract layer; remove it and use the contract's resource", key))
		return
	}
	if config.OwnsInferenceKey(key) {
		p.refuse(layer, where,
			"inference wiring is generated from the user's settings; an application layer cannot declare it")
		return
	}
	p.kind(layer, where, kind)
}

// agentKey decides one agent an application layer declared.
//
// Two kinds are allowed. The contract layer's reserved slot is the
// application's entry agent: a layer merges into it, and the transcript
// path stays the contract layer's. Every other agent has to be one the
// manifest lists, because the manifest is what names the application's
// agents to the rest of the platform — a caller picks one by name
// (RunOptions.AgentID) — and a name that exists only inside a layer is a
// name no caller can see.
//
// The listed agents wire their own hooks — they are their own agents —
// but a turn of one is still a turn of the conversation it ran in: the
// page reloads the transcript, so an agent without a committer answers
// the user and leaves nothing behind. That is required rather than
// merely allowed, and the refusal names the hook to write.
func (p *preflight) agentKey(layer, name string, def agent.Definition) {
	where := "agents." + name
	entry := name == DefaultAgent
	if !entry && !p.listed(name) {
		p.refuse(layer, where, fmt.Sprintf(
			"the manifest does not handle this agent: it names %q as the entry agent and lists %s; every agent an application runs is named there, so a layer and the manifest cannot disagree about what it runs",
			p.app.Agent, listedAgents(p.app)))
		return
	}
	// The entry slot's turn pipeline is the contract layer's: history,
	// the transcript commit and the interrupted-run archive are what
	// every application's entry agent runs, and a layer that rewired
	// them would be replacing the platform's own path.
	if entry {
		for _, slot := range hookSlots(def) {
			if len(slot.hooks) == 0 {
				continue
			}
			p.refuse(layer, where+"."+slot.name,
				"the transcript path (prepare, observe, commit) is the contract layer's; a layer may set the card, the graph, build and policy")
		}
	} else if !commits(def.Commit) {
		p.refuse(layer, where+".commit", fmt.Sprintf(
			"this agent declares no committer: its turns would stream to the page and then be missing from the conversation the page reloads (the entry agent's own path is the contract layer's, so a listed agent wires the platform's committer itself: type %s with the contract's memory and sessions)",
			CommitHookType))
	}
	if len(def.Tools) > 0 {
		p.refuse(layer, where+".tools", "v1 gives applications no tools")
	}
	if def.Engine.Kind != "" {
		p.kind(layer, where+".engine", string(def.Engine.Kind))
	}
	for _, dep := range sortedDeps(def.Engine.Deps) {
		p.depTarget(layer, fmt.Sprintf("%s.engine.deps.%s", where, dep), string(def.Engine.Deps[dep]))
	}
}

// CommitHookType is the hook type that writes a turn to the transcript:
// the contract layer's own committer, which a listed agent has to wire
// the same way (same type, the contract's `mem` and `sessions`).
const CommitHookType = "opencraft.commit"

// commits reports whether an agent declares the transcript committer.
// A committer of another type is not one — the types are the platform's,
// and only this one writes the conversation.
func commits(hooks []agent.Hook) bool {
	for _, hook := range hooks {
		if hook.Type == CommitHookType {
			return true
		}
	}
	return false
}

// hookSlot pairs one hook slot with its name, so a refusal can name it.
type hookSlot struct {
	name  string
	hooks []agent.Hook
}

// hookSlots lists the agent's hook slots in a stable order.
func hookSlots(def agent.Definition) []hookSlot {
	return []hookSlot{
		{name: agent.HookSlotPreparer, hooks: def.Prepare},
		{name: agent.HookSlotObserver, hooks: def.Observe},
		{name: agent.HookSlotReferee, hooks: def.Referees},
		{name: agent.HookSlotCommitter, hooks: def.Commit},
	}
}

// kind applies the allow/deny table to one declared kind.
func (p *preflight) kind(layer, where, kind string) {
	verdict, known := Classify(kind)
	switch {
	case !known:
		p.refuse(layer, where, fmt.Sprintf(
			"kind %q is not a resource kind this build can construct", kind))
	case !verdict.Allowed:
		p.refuse(layer, where, fmt.Sprintf(
			"kind %q is not available to an application: %s", kind, verdict.Reason))
	}
}

// depTarget records one dependency reference. A dep names a resource (or
// a "resource/item" pair) that has to exist in the merged document —
// except the inference wiring, which the host's overlay supplies above
// the application's layers and is therefore allowed to be absent while
// the user's settings are still unconfigured (that state is reported as
// a missing router when the runtime is assembled, not here).
func (p *preflight) depTarget(layer, where, ref string) {
	name, _, ok := resource.Ref(ref).Split()
	if !ok {
		p.refuse(layer, where, fmt.Sprintf("%q is not a resource reference", ref))
		return
	}
	p.deps = append(p.deps, depRef{layer: layer, where: where, ref: name})
}

// document is the second pass: the checks that need the merged document
// — the entry agent, the listed agents, their graphs, the dependency
// targets, and every {file:} reference the merged settings carry.
func (p *preflight) document(merged deploy.Document) {
	agentName := p.app.Agent
	if agentName != DefaultAgent {
		p.refuse(ManifestFile, "agent", fmt.Sprintf(
			"the entry agent is the contract layer's %q slot, which carries the transcript path (commit, observe); the manifest names %q — another agent an application runs is listed in agents: and declared by a layer instead",
			DefaultAgent, agentName))
	}
	definition, ok := merged.Agents[agentName]
	if !ok {
		p.refuse(p.layerOfAgent(agentName), "agents."+agentName, fmt.Sprintf(
			"the manifest's entry agent is not declared by the merged document (the contract layer declares %q)",
			DefaultAgent))
	} else {
		p.graph(p.layerOfAgent(agentName), agentName, definition)
	}
	// The agents the manifest lists are held to what the entry agent is
	// held to — declared, and with a graph to run — because the list is
	// what the page offers a picker and what a turn may name. A listed
	// agent no layer declares is a manifest typo, and the first turn
	// naming it is a bad place to find out.
	for _, name := range p.app.Agents {
		definition, ok := merged.Agents[name]
		if !ok {
			p.refuse(ManifestFile, "agents", fmt.Sprintf(
				"%q is listed as an agent of this application but no layer declares it (the merged document has %s)",
				name, declaredAgents(merged)))
			continue
		}
		p.graph(p.layerOfAgent(name), name, definition)
	}

	for _, dep := range p.deps {
		if _, declared := merged.Resources[dep.ref]; declared {
			continue
		}
		if config.OwnsInferenceKey(dep.ref) {
			continue
		}
		p.refuse(dep.layer, dep.where, fmt.Sprintf(
			"%q names no resource in the merged document (the host generates %s)",
			dep.ref, inferenceKeys()))
	}

	for _, key := range sortedResourceKeys(merged.Resources) {
		where := "resources." + key + ".settings"
		p.settings(p.layerOfResource(key), where, merged.Resources[key].Settings)
	}
}

// graph checks one agent's engine.settings.graph: present, and followed
// like any other reference (a graph document of its own may reference
// node scripts). An application without a graph is refused here rather
// than at assembly, where it would read as a missing file nobody wrote.
func (p *preflight) graph(layer, agentName string, def agent.Definition) {
	prefix := "agents." + agentName + ".engine.settings."
	settings := map[string]json.RawMessage{}
	if len(def.Engine.Settings) > 0 {
		if err := json.Unmarshal(def.Engine.Settings, &settings); err != nil {
			p.refuse(layer, prefix+"graph", "the engine settings are not a JSON object")
			return
		}
	}
	raw, ok := settings["graph"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		p.refuse(layer, prefix+"graph", fmt.Sprintf(
			"agent %q declares no graph; an application deployment has no default graph to fall back on",
			agentName))
	} else {
		p.value(layer, prefix+"graph", raw)
	}
	for _, key := range sortedRawKeys(settings) {
		if key == "graph" {
			continue
		}
		p.value(layer, prefix+key, settings[key])
	}
}

// settings walks one settings blob for {file:} references.
func (p *preflight) settings(layer, where string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	p.value(layer, where, raw)
}

// value walks one JSON value looking for the reference forms core
// defines — the whole-subtree objects {"file": path} and {"embed": name}
// — and follows every file reference it finds.
func (p *preflight) value(layer, where string, raw json.RawMessage) {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		// Settings are validated JSON by the time they land in a
		// document, so this is a value the walker cannot read, not an
		// application mistake: nothing to check inside it.
		return
	}
	p.walk(layer, where, node)
}

// walk recurses through decoded JSON. Any value may hold a reference —
// a graph's node config is where the interesting ones live — so
// everything except a reference object is recursed into.
func (p *preflight) walk(layer, where string, node any) {
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := singleStringKey(value, "file"); ok {
			p.fileRef(layer, where, ref)
			return
		}
		if _, ok := singleStringKey(value, "embed"); ok {
			p.refuse(layer, where,
				"an application layer cannot reference embedded assets ({embed: ...}); the host's own layers are the only embedded content")
			return
		}
		for _, key := range sortedAnyKeys(value) {
			p.walk(layer, where+"."+key, value[key])
		}
	case []any:
		for i, item := range value {
			p.walk(layer, fmt.Sprintf("%s[%d]", where, i), item)
		}
	case string:
		// A string may be a document in its own right (an application
		// may inline its graph instead of referencing a file, and the
		// runtime resolves references inside it the same way). Prose
		// and code are not documents and the conversion below drops
		// them.
		p.followed(layer, where, []byte(value))
	}
}

// fileRef checks one referenced path and, when it names a document,
// follows it: a graph references its node scripts, and those references
// resolve against the content root exactly like the first one.
func (p *preflight) fileRef(layer, where, ref string) {
	site := refSite{layer: layer, where: where, ref: ref}
	full, ok := p.inside(site)
	if !ok {
		return
	}
	if !p.regular(site, full, "referenced file") || !p.straight(site, full) {
		return
	}
	if !documentExt(full) || p.seen[full] {
		return
	}
	if p.scanned >= maxScannedRefs {
		p.refuse(layer, where, site.describe(fmt.Sprintf(
			"the document chain exceeds %d referenced documents", maxScannedRefs)))
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		p.refuse(layer, where,
			site.describe("the referenced document cannot be read: "+err.Error()))
		return
	}
	if info.Size() > maxScannedBytes {
		p.refuse(layer, where, site.describe(fmt.Sprintf(
			"the referenced document exceeds %d bytes", int64(maxScannedBytes))))
		return
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		p.refuse(layer, where,
			site.describe("the referenced document cannot be read: "+err.Error()))
		return
	}
	p.seen[full] = true
	p.scanned++
	// Inside the followed document the reference itself becomes the
	// position: a refusal about a node script names the graph and the
	// path inside it that asked for it.
	p.followed(layer, ref, raw)
}

// followed walks the content of one referenced or inlined document. It
// is read as configuration (YAML or JSON, converted by the same rule the
// loader uses) and only a mapping or a sequence counts as a document: a
// script, a prompt or a stylesheet has nothing to follow, and reading
// one as YAML would only invent references out of someone's prose.
func (p *preflight) followed(layer, where string, raw []byte) {
	jsonBytes, err := utils.ToJSON(raw)
	if err != nil {
		return
	}
	var node any
	if err := json.Unmarshal(jsonBytes, &node); err != nil {
		return
	}
	switch node.(type) {
	case map[string]any, []any:
		p.walk(layer, where, node)
	}
}

// refuse records one refusal.
func (p *preflight) refuse(layer, key, reason string) {
	p.refusals.List = append(p.refusals.List,
		Refusal{Layer: layer, Key: key, Reason: reason})
}

// layerOfResource names the layer that declared one merged resource key:
// the preflight merges the contract layer and the application's layers,
// and a refusal must not claim an application file declared a key the
// host did.
func (p *preflight) layerOfResource(key string) string {
	return layerName(p.provenance.Resources[key])
}

// layerOfAgent names the layer that declared one merged agent.
func (p *preflight) layerOfAgent(name string) string {
	return layerName(p.provenance.Agents[name])
}

// layerName turns one provenance entry into a layer name, empty for the
// host's own layers (they sit below the application band).
func layerName(ref deploy.LayerRef) string {
	if ref.Priority < config.AppLayerPriorityBase {
		return ""
	}
	return ref.Name
}

// documentExt reports whether a referenced path is a document the scan
// should follow: configuration in this repository is YAML or JSON.
func documentExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// singleStringKey returns the value of key when value is an object whose
// only entry is key and that entry is a string: the shape core defines
// as a reference.
func singleStringKey(value map[string]any, key string) (string, bool) {
	if len(value) != 1 {
		return "", false
	}
	raw, ok := value[key]
	if !ok {
		return "", false
	}
	text, ok := raw.(string)
	return text, ok
}

// inferenceKeys names the resources the host generates above an
// application's layers, for the refusal that says a dep target is
// missing but allowed to be.
func inferenceKeys() string { return "infer, router and every provider.*" }

// sortedResourceKeys returns a resource map's keys in a stable order, so
// a refusal list reads the same on every run.
func sortedResourceKeys(res resource.Resources) []string {
	keys := make([]string, 0, len(res))
	for key := range res {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedAgentNames returns an agent map's keys in a stable order.
func sortedAgentNames(agents map[string]agent.Definition) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedDeps returns a dependency map's keys in a stable order.
func sortedDeps(deps resource.Deps) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedAnyKeys returns a decoded JSON object's keys in a stable order.
func sortedAnyKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedRawKeys returns a decoded JSON object's keys in a stable order.
func sortedRawKeys(value map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
