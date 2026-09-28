package pluginagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

type fakeHost struct {
	specs    []agent.ToolSpec
	servers  []agent.MCPServer
	invokeFn func(ctx context.Context, pluginID, method string, args json.RawMessage) (json.RawMessage, error)

	watchMu  sync.Mutex
	watchers map[int]func()
	nextSub  int
}

func (f *fakeHost) ToolSpecs() []agent.ToolSpec {
	f.watchMu.Lock()
	defer f.watchMu.Unlock()
	return append([]agent.ToolSpec(nil), f.specs...)
}

func (f *fakeHost) MCPServers() []agent.MCPServer {
	return f.servers
}
func (f *fakeHost) Invoke(
	ctx context.Context,
	pluginID, method string,
	args json.RawMessage,
) (json.RawMessage, error) {
	if f.invokeFn != nil {
		return f.invokeFn(ctx, pluginID, method, args)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeHost) Watch(fn func()) (cancel func()) {
	f.watchMu.Lock()
	if f.watchers == nil {
		f.watchers = make(map[int]func())
	}
	key := f.nextSub
	f.nextSub++
	f.watchers[key] = fn
	f.watchMu.Unlock()
	return func() {
		f.watchMu.Lock()
		delete(f.watchers, key)
		f.watchMu.Unlock()
	}
}

// setSpecs replaces the manifest scan and wakes the watchers, the way a
// registry mutation does.
func (f *fakeHost) setSpecs(specs ...agent.ToolSpec) {
	f.watchMu.Lock()
	f.specs = specs
	watchers := make([]func(), 0, len(f.watchers))
	for _, fn := range f.watchers {
		watchers = append(watchers, fn)
	}
	f.watchMu.Unlock()
	for _, fn := range watchers {
		fn()
	}
}

type countingRegistrar struct {
	adds int
}

func (c *countingRegistrar) Add(tool.Tool) error {
	c.adds++
	return nil
}

func (c *countingRegistrar) Remove(string) {}

func kraftSpec() agent.ToolSpec {
	return agent.ToolSpec{
		PluginID:    "hello",
		Name:        "ping",
		Description: "Ping the plugin",
		Method:      "ping",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func TestSourceExposesKraftTools(t *testing.T) {
	host := &fakeHost{
		specs: []agent.ToolSpec{kraftSpec()},
		invokeFn: func(
			_ context.Context, pluginID, method string, _ json.RawMessage,
		) (json.RawMessage, error) {
			if pluginID != "hello" || method != "ping" {
				return nil, errors.New("wrong method routed")
			}
			return json.RawMessage(`"pong"`), nil
		},
	}
	src, err := newSource(t.Context(), host)
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg, err := tool.NewRegistry([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "hello__ping" {
		t.Fatalf("registry names = %v", names)
	}
	got, ok := reg.Get("hello__ping")
	if !ok {
		t.Fatal("hello__ping missing from registry")
	}
	if def := got.Definition(); def.Description != "[plugin hello] Ping the plugin" {
		t.Fatalf("definition = %+v", def)
	}
	res, err := got.Execute(t.Context(), `{"x":1}`)
	if err != nil || res.Text() != "pong" {
		t.Fatalf("Execute = (%q, %v)", res, err)
	}
}

func TestSourceAttachDoesNotReRegisterKraftTools(t *testing.T) {
	src, err := newSource(t.Context(), &fakeHost{
		specs: []agent.ToolSpec{kraftSpec()},
	})
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg := &countingRegistrar{}
	src.Attach(reg)
	if reg.adds != 0 {
		t.Fatalf("Attach registered %d kraft tools, want 0", reg.adds)
	}
}

func TestSourceAssemblyExposesKraftToolsOnce(t *testing.T) {
	src, err := newSource(t.Context(), &fakeHost{
		specs: []agent.ToolSpec{kraftSpec()},
	})
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	asm, err := tool.NewAssembly([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewAssembly: %v", err)
	}
	defs := asm.Catalog().Definitions()
	if len(defs) != 1 || defs[0].Name != "hello__ping" {
		t.Fatalf("assembly definitions = %+v, want one hello__ping", defs)
	}
}

func TestSourceEmptyHost(t *testing.T) {
	src, err := newSource(t.Context(), &fakeHost{})
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg, err := tool.NewRegistry([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(reg.Names()) != 0 {
		t.Fatalf("empty host must contribute no tools, got %v", reg.Names())
	}
}

func TestSourceSanitizesPluginIDInToolName(t *testing.T) {
	host := &fakeHost{
		specs: []agent.ToolSpec{{
			PluginID: "my.plugin",
			Name:     "ping",
			Method:   "ping",
		}},
	}
	src, err := newSource(t.Context(), host)
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg, err := tool.NewRegistry([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "my_plugin__ping" {
		t.Fatalf("registry names = %v, want [my_plugin__ping]", names)
	}
}

// recordingRegistrar is the live-registry stand-in: it keeps the tools a
// source published, and rejects a duplicate name the way the real
// registry's default conflict policy does.
type recordingRegistrar struct {
	mu      sync.Mutex
	tools   map[string]tool.Tool
	added   []string
	removed []string
}

func newRecordingRegistrar() *recordingRegistrar {
	return &recordingRegistrar{tools: make(map[string]tool.Tool)}
}

func (r *recordingRegistrar) Add(t tool.Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Definition().Name
	if _, ok := r.tools[name]; ok {
		return errors.New("duplicate tool " + name)
	}
	r.tools[name] = t
	r.added = append(r.added, name)
	return nil
}

func (r *recordingRegistrar) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return
	}
	delete(r.tools, name)
	r.removed = append(r.removed, name)
}

func (r *recordingRegistrar) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *recordingRegistrar) definition(name string) message.ToolDefinition {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tools[name]; ok {
		return t.Definition()
	}
	return message.ToolDefinition{}
}

// TestSourceRepublishesIntoTheLiveRegistry pins the agent face of the
// register clock (see the charter's FaceRefreshes): a plugin installed,
// updated or disabled while a turn is running reaches the next round's
// definitions, without waiting for an assembly. The source reconciles
// the registry it was attached to, so a tool the plugin gained appears, a
// changed schema replaces the definition, a tool it lost goes, and Close
// unsubscribes.
func TestSourceRepublishesIntoTheLiveRegistry(t *testing.T) {
	host := &fakeHost{specs: []agent.ToolSpec{kraftSpec()}}
	src, err := newSource(t.Context(), host)
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg := newRecordingRegistrar()
	// The assembly reads Tools() into the registry before it hands the
	// registrar over (core's NewAssembly), so the starting set counts as
	// published.
	for _, published := range src.Tools() {
		if err := reg.Add(published); err != nil {
			t.Fatalf("seed registrar: %v", err)
		}
	}
	src.Attach(reg)
	if got := reg.names(); !slices.Equal(got, []string{"hello__ping"}) {
		t.Fatalf("initial registry content = %v", got)
	}

	// The plugin gained a second tool.
	second := kraftSpec()
	second.Name = "pair"
	second.Method = "pair"
	host.setSpecs(kraftSpec(), second)
	if got := reg.names(); !slices.Equal(got, []string{"hello__pair", "hello__ping"}) {
		t.Fatalf("after a tool was added: %v", got)
	}

	// A changed schema replaces the definition instead of colliding.
	renamed := kraftSpec()
	renamed.Description = "Ping the plugin again"
	host.setSpecs(renamed, second)
	if got := reg.names(); !slices.Equal(got, []string{"hello__pair", "hello__ping"}) {
		t.Fatalf("after a description change: %v", got)
	}
	if got := reg.definition("hello__ping").Description; got !=
		"[plugin hello] Ping the plugin again" {
		t.Fatalf("registry kept the old definition: %q", got)
	}

	// The plugin lost the first tool.
	host.setSpecs(second)
	if got := reg.names(); !slices.Equal(got, []string{"hello__pair"}) {
		t.Fatalf("after a tool was removed: %v", got)
	}

	// Disabling the plugin empties the published set.
	host.setSpecs()
	if got := reg.names(); len(got) != 0 {
		t.Fatalf("after the last plugin went away: %v", got)
	}

	// Close unsubscribes: a later mutation publishes nothing.
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	host.setSpecs(kraftSpec())
	if got := reg.names(); len(got) != 0 {
		t.Fatalf("a closed source published %v", got)
	}
}

// TestSourceBoundsTheAggregateToolBudget pins the cap above the
// per-plugin limits: one 32 KiB schema is the per-plugin bound
// (capabilities/plugins' agent.tool row), and twenty plugins at that
// bound would hand the model 640 KiB of schemas in one round. The scan
// keeps what fits, in registry order, and drops the rest — determinism
// matters: the same registry has to keep the same tools across
// republishes, not whichever ones a map happened to yield.
func TestSourceBoundsTheAggregateToolBudget(t *testing.T) {
	big := kraftSpec()
	big.InputSchema = json.RawMessage(`{"pad":"` +
		strings.Repeat("x", 32<<10) + `"}`)
	var specs []agent.ToolSpec
	for i := 0; i < 32; i++ {
		spec := big
		spec.Name = fmt.Sprintf("tool%02d", i)
		spec.Method = spec.Name
		specs = append(specs, spec)
	}
	kept, dropped := boundKraftSpecs(specs)
	if dropped == 0 {
		t.Fatalf("32 x 32 KiB schemas were all kept: the aggregate "+
			"budget %d bytes is not applied", maxKraftDefinitionBytes)
	}
	if len(kept)+dropped != len(specs) {
		t.Fatalf("kept %d + dropped %d != %d specs",
			len(kept), dropped, len(specs))
	}
	bytes := 0
	for _, spec := range kept {
		bytes += len(spec.Name) + len(spec.Description) + len(spec.InputSchema)
	}
	if bytes > maxKraftDefinitionBytes {
		t.Fatalf("kept %d bytes, over the %d-byte budget",
			bytes, maxKraftDefinitionBytes)
	}
	// Order is the registry's, so the kept set is a prefix: the same
	// registry always keeps the same tools.
	for i, spec := range kept {
		if want := fmt.Sprintf("tool%02d", i); spec.Name != want {
			t.Fatalf("kept[%d] = %q, want %q: the budget is not applied "+
				"in registry order", i, spec.Name, want)
		}
	}

	// A registry under the per-plugin count bound keeps everything.
	small := make([]agent.ToolSpec, 0, maxKraftTools)
	for i := 0; i < maxKraftTools; i++ {
		spec := kraftSpec()
		spec.Name = fmt.Sprintf("tool%03d", i)
		spec.Method = spec.Name
		small = append(small, spec)
	}
	if _, dropped := boundKraftSpecs(small); dropped != 0 {
		t.Fatalf("a registry at the count bound dropped %d tools", dropped)
	}
}

// TestSourceWarnsOnceWhenTheBudgetTruncates covers the warning: the
// truncation is invisible in the tool list (the tools simply are not
// there), so the host logs it — once per source, not once per republish,
// because a registry that stays over the bound republishes on every
// mutation.
func TestSourceWarnsOnceWhenTheBudgetTruncates(t *testing.T) {
	var specs []agent.ToolSpec
	for i := 0; i < maxKraftTools+8; i++ {
		spec := kraftSpec()
		spec.Name = fmt.Sprintf("tool%03d", i)
		spec.Method = spec.Name
		specs = append(specs, spec)
	}
	host := &fakeHost{specs: specs}
	records := logcapture.Install(t)

	src, err := newSource(t.Context(), host)
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	if got := len(src.Tools()); got != maxKraftTools {
		t.Fatalf("published %d tools, want the %d-tool budget",
			got, maxKraftTools)
	}
	if got := logLines(records, "aggregate tool budget"); got != 1 {
		t.Fatalf("construction logged the truncation %d times, want once", got)
	}

	// Two republishes later, still one line: the source remembers.
	reg := newRecordingRegistrar()
	src.Attach(reg)
	host.setSpecs(append(specs, agent.ToolSpec{
		PluginID: "hello", Name: "late", Method: "late",
		Description: "Late tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})...)
	host.setSpecs(specs...)
	if got := logLines(records, "aggregate tool budget"); got != 1 {
		t.Fatalf("the truncation was logged %d times across republishes, "+
			"want once", got)
	}
}

// logLines counts the captured records whose body contains want.
func logLines(records *logcapture.Recorder, want string) int {
	n := 0
	for _, body := range records.Bodies() {
		if strings.Contains(body, want) {
			n++
		}
	}
	return n
}
