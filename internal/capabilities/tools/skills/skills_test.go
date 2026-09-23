package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	skillspkg "github.com/GizClaw/opencraft/internal/capabilities/skills"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

func confirmCtx(t *testing.T, choice string, cancelled bool) context.Context {
	t.Helper()
	meta := map[string]string{}
	if cancelled {
		meta[interact.MetaStatus] = string(interact.ReplyCancelled)
	} else {
		meta[interact.MetaChoice] = choice
	}
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{Metadata: meta}, nil
		},
	})
}

func newTestService(t *testing.T) *skillspkg.Service {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	scan := filepath.Join(home, ".agents", "skills")
	write := func(name, desc string) {
		dir := filepath.Join(scan, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + desc +
			"\n---\n\n# " + name + "\nFull instructions for " + name + ".\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("review", "review code and docs for quality")
	write("plan", "build execution plans")
	return skillspkg.NewService(context.Background(), skillspkg.Options{
		Enabled: true,
	})
}

func TestSkillSearchTool(t *testing.T) {
	tool := searchTool{newTestService(t)}
	out, err := tool.Execute(context.Background(), `{"query": "review"}`)
	if err != nil {
		t.Fatal(err)
	}
	var hits []struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		Path        string  `json:"path"`
		Scope       string  `json:"scope"`
		Score       float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(out.Text()), &hits); err != nil {
		t.Fatalf("search output %q: %v", out, err)
	}
	if len(hits) == 0 || hits[0].Name != "review" {
		t.Fatalf("skill_search = %+v, want review first", hits)
	}
	if hits[0].Description == "" || hits[0].Path == "" {
		t.Fatalf("skill_search must return metadata: %+v", hits[0])
	}
	if hits[0].Score <= 0 {
		t.Fatalf("skill_search must expose the BM25 score: %+v", hits[0])
	}

	// Empty query lists the catalog (bounded by limit).
	out, err = tool.Execute(context.Background(), `{"limit": 1}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out.Text()), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("empty query limit 1 = %d hits, want 1", len(hits))
	}

	// Unknown arguments are rejected.
	if _, err := tool.Execute(context.Background(), `{"nope": 1}`); err == nil {
		t.Fatal("unknown argument should fail")
	}
}

func TestSkillCreateModifyTools(t *testing.T) {
	svc := newTestService(t)
	create := createTool{svc}
	out, err := create.Execute(confirmCtx(t, "yes", false),
		`{"name":"qa","description":"run the qa checklist","body":"## Steps\n1. Build.\n2. Test.\n","files":{"scripts/run.py":"#!/usr/bin/env python3\nprint('ok')\n"},"executable":["scripts/run.py"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), "qa") {
		t.Fatalf("create output = %q", out)
	}
	meta, body, err := svc.ReadFull("qa")
	if err != nil {
		t.Fatalf("created skill not discoverable: %v", err)
	}
	if meta.Description != "run the qa checklist" ||
		!strings.Contains(body, "2. Test.") {
		t.Fatalf("created skill = %+v / %q", meta, body)
	}
	script := filepath.Join(filepath.Dir(meta.Path), "scripts", "run.py")
	if data, err := os.ReadFile(script); err != nil ||
		!strings.Contains(string(data), "print('ok')") {
		t.Fatalf("created script = %q, %v", data, err)
	}
	if info, err := os.Stat(script); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("script not executable: %v", err)
	}

	modify := modifyTool{svc}
	out, err = modify.Execute(confirmCtx(t, "yes", false),
		`{"name":"qa","body":"## Steps\n1. Build.\n2. Test.\n3. Ship.\n","files":{"scripts/run.py":"#!/usr/bin/env python3\nprint('v2')\n"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), "qa") {
		t.Fatalf("modify output = %q", out)
	}
	meta, body, err = svc.ReadFull("qa")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "run the qa checklist" {
		t.Fatalf("modify dropped description: %q", meta.Description)
	}
	if !strings.Contains(body, "3. Ship.") {
		t.Fatalf("modified body = %q", body)
	}

	// Partial patch mode edits just one hunk of SKILL.md.
	out, err = modify.Execute(confirmCtx(t, "yes", false),
		`{"name":"qa","patch":"*** Begin Patch\n*** Update File: SKILL.md\n@@\n-3. Ship.\n+3. Ship to prod.\n*** End Patch\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), "SKILL.md") {
		t.Fatalf("patch output = %q", out)
	}
	_, body, err = svc.ReadFull("qa")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "3. Ship to prod.") {
		t.Fatalf("patched body = %q", body)
	}

	// patch and body are mutually exclusive.
	if _, err := modify.Execute(confirmCtx(t, "yes", false),
		`{"name":"qa","body":"x","patch":"*** Begin Patch\n*** End Patch\n"}`); err == nil {
		t.Fatal("patch + body must be rejected")
	}

	// Invalid names are rejected.
	if _, err := create.Execute(confirmCtx(t, "yes", false),
		`{"name":"Bad Name","description":"x","body":"y"}`); err == nil {
		t.Fatal("invalid name must fail")
	}
	// Unknown arguments are rejected.
	if _, err := modify.Execute(confirmCtx(t, "yes", false),
		`{"name":"qa","body":"x","nope":1}`); err == nil {
		t.Fatal("unknown argument must fail")
	}
}

func TestSkillCreateRequiresConfirmation(t *testing.T) {
	svc := newTestService(t)
	create := createTool{svc}
	out, err := create.Execute(confirmCtx(t, "", true),
		`{"name":"sneaky","description":"d","body":"## X\n"}`)
	if err != nil {
		t.Fatalf("Execute(cancelled): %v", err)
	}
	if !strings.Contains(out.Text(), `"cancelled":true`) {
		t.Fatalf("cancelled output = %q", out)
	}
	if _, _, err := svc.ReadFull("sneaky"); err == nil {
		t.Fatal("cancelled create must not write a skill")
	}
}

func TestSkillReadTool(t *testing.T) {
	tool := readTool{newTestService(t)}
	out, err := tool.Execute(context.Background(), `{"name": "plan"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), "Full instructions for plan") {
		t.Fatalf("skill_read = %q, want full body", out)
	}
	if _, err := tool.Execute(context.Background(), `{"name": "missing"}`); err == nil {
		t.Fatal("skill_read(missing) should fail")
	}
}

// countingLifecycle records the usage events the tools produce. The
// embedded lifecycle supplies the read half (no decisions, no
// archives), so the tests below pin the write path only.
type countingLifecycle struct {
	skillusage.Lifecycle
	mu     sync.Mutex
	events []skillusage.Event
}

func newCountingLifecycle() *countingLifecycle {
	return &countingLifecycle{Lifecycle: skillusage.EmptyLifecycle()}
}

func (c *countingLifecycle) Empty() bool { return false }

func (c *countingLifecycle) Record(_ context.Context, event skillusage.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *countingLifecycle) snapshot() []skillusage.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]skillusage.Event(nil), c.events...)
}

func hasSkillNamed(list []skillspkg.SkillMetadata, name string) bool {
	for _, sk := range list {
		if sk.Name == name {
			return true
		}
	}
	return false
}

// TestSkillReadRecordsUsage pins the second usage write point: the
// skill_read tool is how the model pulls a skill in on purpose, so the
// event has to carry the run it happened in.
func TestSkillReadRecordsUsage(t *testing.T) {
	svc := newTestService(t)
	recorder := newCountingLifecycle()
	svc.SetLifecycle(recorder, config.SkillLifecycleConfig{
		Enabled: true, StaleAfterDays: 45, MinUses: 3, UsageWindowDays: 90,
	}, t.TempDir())

	// flowcraft injects the run identity into the tool context; without
	// it the event would carry no run / conversation id.
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", RunID: "run-9", ConversationID: "s-3"},
	})
	out, err := readTool{svc}.Execute(ctx, `{"name": "plan"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), "Full instructions for plan") {
		t.Fatalf("skill_read = %q, want full body", out)
	}

	events := recorder.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %+v, want exactly one", events)
	}
	got := events[0]
	if got.Name != "plan" || got.Scope != "user" || got.RunID != "run-9" ||
		got.ConversationID != "s-3" || got.UsedAt.IsZero() {
		t.Fatalf("event = %+v", got)
	}

	// A read that failed loaded nothing, so it is not a use.
	if _, err := (readTool{svc}).Execute(ctx, `{"name": "missing"}`); err == nil {
		t.Fatal("skill_read(missing) should fail")
	}
	if events = recorder.snapshot(); len(events) != 1 {
		t.Fatalf("failed read recorded usage: %+v", events)
	}
}

// TestSkillSearchHidesRetiredSkills pins the tool side of the
// retirement filter: the catalog listing and the ranked search are both
// ways the model is pointed at a skill, so a retired skill must not come
// back through them.
func TestSkillSearchHidesRetiredSkills(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(ctx, handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := skillusage.Attach(handle)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetLifecycle(store, config.SkillLifecycleConfig{
		Enabled: true, StaleAfterDays: 45, MinUses: 3, UsageWindowDays: 90,
	}, t.TempDir())
	if !hasSkillNamed(svc.Available(), "plan") {
		t.Fatal("plan not discovered")
	}
	// The page writes decisions straight to the store, behind the
	// registry's back; the registry picks them up within its cache TTL,
	// or immediately after a reload as here.
	if _, err := store.SetRetired(ctx, "plan", "user", true); err != nil {
		t.Fatal(err)
	}
	svc.Reload()

	tool := searchTool{svc}
	out, err := tool.Execute(ctx, `{"limit": 10}`)
	if err != nil {
		t.Fatal(err)
	}
	listed := decodeHits(t, out.Text())
	if hasHit(listed, "plan") {
		t.Fatalf("catalog listing offers a retired skill: %s", out.Text())
	}
	if !hasHit(listed, "review") {
		t.Fatalf("catalog listing lost the live skills: %s", out.Text())
	}
	out, err = tool.Execute(ctx, `{"query": "build execution plans"}`)
	if err != nil {
		t.Fatal(err)
	}
	// The builtins legitimately match the query; the retired skill is
	// the one that must not be ranked.
	if ranked := decodeHits(t, out.Text()); hasHit(ranked, "plan") {
		t.Fatalf("search ranked a retired skill: %s", out.Text())
	}
}

// searchHit is the slice of the skill_search JSON these tests inspect.
type searchHit struct {
	Name string `json:"name"`
}

// decodeHits parses one skill_search result list. A search with no hits
// marshals as null, which unmarshals into a nil slice.
func decodeHits(t *testing.T, out string) []searchHit {
	t.Helper()
	var hits []searchHit
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("search output %q: %v", out, err)
	}
	return hits
}

func hasHit(list []searchHit, name string) bool {
	for _, hit := range list {
		if hit.Name == name {
			return true
		}
	}
	return false
}
