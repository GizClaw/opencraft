package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillspkg "github.com/GizClaw/opencraft/internal/skills"
)

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
	return skillspkg.NewService(skillspkg.Options{
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
