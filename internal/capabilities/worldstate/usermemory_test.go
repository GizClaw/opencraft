package worldstate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// newUserMemory opens a migrated user database and returns a binding
// over it, the shape the deploy graph hands the worldstate hook.
func newUserMemory(
	t *testing.T, settings config.UserMemorySettings,
) *userstore.Binding {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := userstore.Attach(handle)
	if err != nil {
		t.Fatalf("attach memory: %v", err)
	}
	resolved, err := settings.Resolve()
	if err != nil {
		t.Fatalf("resolve settings: %v", err)
	}
	return &userstore.Binding{Memory: store, Config: resolved}
}

func addFact(t *testing.T, binding *userstore.Binding, work, text, scope string) {
	t.Helper()
	fact := userstore.Fact{Text: text, Scope: scope}
	if scope == "" {
		fact.Scope = userstore.ScopeGlobal
	}
	if scope == userstore.ScopeWorkspace {
		fact.Workspace = work
	}
	if _, err := binding.Memory.Add(context.Background(), fact); err != nil {
		t.Fatalf("add fact: %v", err)
	}
}

// TestUserMemorySectionRendersFacts covers the injected section: the
// workspace filter, the newest-first order and the telemetry that makes
// the injected size visible.
func TestUserMemorySectionRendersFacts(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	binding := newUserMemory(t, config.DefaultUserMemorySettings())

	// A global fact is visible in every workspace; a workspace fact of
	// another project is not.
	if _, err := binding.Memory.Add(ctx, userstore.Fact{
		Text:  "Haivivi prefers Chinese commit messages",
		Scope: userstore.ScopeGlobal,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Memory.Add(ctx, userstore.Fact{
		Text:      "another project's fact",
		Scope:     userstore.ScopeWorkspace,
		Workspace: filepath.Join(t.TempDir(), "elsewhere"),
	}); err != nil {
		t.Fatal(err)
	}
	addFact(t, binding, work, "the deploy lane runs wails3 task package",
		userstore.ScopeWorkspace)

	service := New(Options{WorkBase: work})
	service.SetUserMemory(binding)

	recorder := logcapture.Install(t)
	section, err := service.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body := section.Content.Text()
	if section.ID != "user_memory" {
		t.Fatalf("section id = %q", section.ID)
	}
	if section.Role != message.RoleUser {
		t.Fatalf("section role = %q, want user", section.Role)
	}
	if !strings.Contains(body, "Haivivi prefers Chinese commit messages") {
		t.Fatalf("global fact missing from section: %q", body)
	}
	if !strings.Contains(body, "the deploy lane runs wails3 task package") {
		t.Fatalf("workspace fact missing from section: %q", body)
	}
	if strings.Contains(body, "another project's fact") {
		t.Fatalf("foreign workspace fact leaked into the section: %q", body)
	}

	injected, ok := findLog(recorder, "worldstate: user memory injected")
	if !ok {
		t.Fatalf("no injection telemetry: %v", recorder.Bodies())
	}
	if got := logcapture.Attribute(injected, "facts"); got != "2" {
		t.Fatalf("telemetry facts = %q, want 2", got)
	}
	if got := logcapture.Attribute(injected, "bytes"); got == "" || got == "0" {
		t.Fatalf("telemetry bytes = %q, want the injected size", got)
	}
}

// TestUserMemorySectionBounds covers the injection budget: the count cap
// is applied by the store query and the byte cap drops the oldest facts.
func TestUserMemorySectionBounds(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	settings := config.DefaultUserMemorySettings()
	settings.InjectMaxItems = 2
	settings.InjectMaxChars = config.UserMemoryMinInjectMaxChars
	binding := newUserMemory(t, settings)

	addFact(t, binding, work, "third fact, dropped by the count cap",
		userstore.ScopeWorkspace)
	addFact(t, binding, work, "second fact that survives the caps",
		userstore.ScopeWorkspace)
	addFact(t, binding, work, "newest fact that survives both caps",
		userstore.ScopeWorkspace)

	service := New(Options{WorkBase: work})
	service.SetUserMemory(binding)
	section, err := service.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body := section.Content.Text()
	if strings.Contains(body, "third fact") {
		t.Fatalf("count cap not applied: %q", body)
	}
	if !strings.Contains(body, "newest fact") ||
		!strings.Contains(body, "second fact") {
		t.Fatalf("newest two facts must survive the count cap: %q", body)
	}
	// The omitted count reports the count cap as well as the byte
	// budget: a section cut by either bound must say it is partial.
	if !strings.Contains(body, "(1 older fact(s) not shown)") {
		t.Fatalf("count-cap omission not reported: %q", body)
	}

	// The byte budget is the second bound: a long fact list must be cut
	// down to the configured size.
	small := config.DefaultUserMemorySettings()
	small.InjectMaxChars = config.UserMemoryMinInjectMaxChars
	long := newUserMemory(t, small)
	for i := 0; i < 12; i++ {
		addFact(t, long, work,
			strings.Repeat("a durable fact that takes room ", 6)+
				string(rune('a'+i)),
			userstore.ScopeWorkspace)
	}
	bounded := New(Options{WorkBase: work})
	bounded.SetUserMemory(long)
	section, err = bounded.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	header, err := render(userMemoryTmpl, userMemoryData{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(section.Content.Text()); got > small.InjectMaxChars+len(header) {
		t.Fatalf("section is %d bytes, over the %d-byte budget (+%d header)",
			got, small.InjectMaxChars, len(header))
	}

	// A single fact larger than the whole budget is omitted, not
	// truncated: the section stays empty rather than misleading.
	huge := newUserMemory(t, small)
	addFact(t, huge, work, strings.Repeat("x", small.InjectMaxChars+64),
		userstore.ScopeWorkspace)
	only := New(Options{WorkBase: work})
	only.SetUserMemory(huge)
	section, err = only.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if section.Content.Text() != "" {
		t.Fatalf("oversized fact was injected: %q", section.Content.Text())
	}
}

// TestUserMemorySectionDegrades covers the "nothing to inject" cases: no
// binding at all (a runtime without user.db), a disabled feature, and a
// store with no database behind it.
func TestUserMemorySectionDegrades(t *testing.T) {
	ctx := context.Background()
	service := New(Options{WorkBase: t.TempDir()})
	section, err := service.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if section.Content.Text() != "" {
		t.Fatalf("section = %q, want empty", section.Content.Text())
	}

	disabled := false
	binding := newUserMemory(t, config.UserMemorySettings{Enabled: &disabled})
	service.SetUserMemory(binding)
	section, err = service.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if section.Content.Text() != "" {
		t.Fatalf("disabled section = %q, want empty", section.Content.Text())
	}

	service.SetUserMemory(&userstore.Binding{
		Memory: userstore.Empty(),
		Config: binding.Config,
	})
	section, err = service.userMemorySection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if section.Content.Text() != "" {
		t.Fatalf("empty-store section = %q, want empty", section.Content.Text())
	}
}

// TestRenderToBoardPlacesUserMemory covers the ordering contract: the
// memory section rides the stable-first user context, right after
// AGENTS.md, and not the per-turn tail — facts change on an explicit
// user action, not once per turn, so the prefix stays cacheable.
func TestRenderToBoardPlacesUserMemory(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	write(t, filepath.Join(work, "AGENTS.md"), "project rules")
	binding := newUserMemory(t, config.DefaultUserMemorySettings())
	addFact(t, binding, work, "a durable fact", userstore.ScopeWorkspace)

	service := New(Options{WorkBase: work})
	service.SetUserMemory(binding)
	board := agent.NewBoard()
	if err := service.RenderToBoard(
		ctx, "assistant", "s-u1", "hello", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	raw := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		t.Fatalf("sections = %q: %v", raw, err)
	}
	ids := ids(sections)
	agentsAt, memoryAt := -1, -1
	for i, id := range ids {
		switch id {
		case "agents_md":
			agentsAt = i
		case "user_memory":
			memoryAt = i
		}
	}
	if memoryAt < 0 || agentsAt < 0 {
		t.Fatalf("sections = %v, want agents_md and user_memory", ids)
	}
	if memoryAt != agentsAt+1 {
		t.Fatalf("user_memory at %d, want right after agents_md at %d: %v",
			memoryAt, agentsAt, ids)
	}
	if tail := board.GetVarString("world.tail_block"); strings.Contains(
		tail, "a durable fact",
	) {
		t.Fatalf("memory leaked into the per-turn tail: %q", tail)
	}
}

// findLog returns the first captured record whose body matches.
func findLog(
	recorder *logcapture.Recorder, body string,
) (sdklog.Record, bool) {
	for _, rec := range recorder.Records() {
		if rec.Body().AsString() == body {
			return rec, true
		}
	}
	return sdklog.Record{}, false
}
