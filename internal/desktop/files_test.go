package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchFiles(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"cmd/opencraft/main.go",
		"internal/tools/exec.go",
		"internal/tools/exec_test.go",
		"internal/skills/skill.md",
		"node_modules/pkg/index.js",
		".github/workflows/ci.yml",
		".opencraft/config/opencraft.yaml",
		"README.md",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	app := &App{workDir: root}
	hits, err := app.SearchFiles("exec", 50)
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	got := make([]string, 0, len(hits))
	for _, h := range hits {
		got = append(got, h.Path)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"internal/tools/exec.go",
		"internal/tools/exec_test.go",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("SearchFiles(exec) missing %q; got:\n%s", want, joined)
		}
	}
	for _, banned := range []string{"node_modules", ".github", ".opencraft"} {
		if strings.Contains(joined, banned) {
			t.Errorf("SearchFiles(exec) leaked skipped dir %q:\n%s", banned, joined)
		}
	}
	if strings.Contains(joined, "../") || strings.Contains(joined, root) {
		t.Errorf("SearchFiles returned absolute/escaping paths:\n%s", joined)
	}
}

func TestSearchFilesEmptyQuery(t *testing.T) {
	app := &App{workDir: t.TempDir()}
	hits, err := app.SearchFiles("  ", 10)
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("empty query must return no hits, got %d", len(hits))
	}
}
