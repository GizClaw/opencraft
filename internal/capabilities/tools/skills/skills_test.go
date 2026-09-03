package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	skillspkg "github.com/GizClaw/opencraft/internal/capabilities/skills"
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
	root := t.TempDir()
	scan := filepath.Join(root, ".agents", "skills")
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
		WorkBase: root, Enabled: true,
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
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
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
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
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
	if !strings.Contains(out, "qa") {
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
	if !strings.Contains(out, "qa") {
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
	if !strings.Contains(out, "SKILL.md") {
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
	if !strings.Contains(out, `"cancelled":true`) {
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
	if !strings.Contains(out, "Full instructions for plan") {
		t.Fatalf("skill_read = %q, want full body", out)
	}
	if _, err := tool.Execute(context.Background(), `{"name": "missing"}`); err == nil {
		t.Fatal("skill_read(missing) should fail")
	}
}
