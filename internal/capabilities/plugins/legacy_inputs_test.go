package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestLegacyInputWarningsAreLoggedOnce covers the promises the
// compatibility layer makes for a manifest an older build wrote: the
// retired names and the stale contributes segment are dropped so the
// plugin keeps loading, and the author gets exactly one log line per
// (plugin, input) — not one per scan, because List() and the agent host
// re-parse manifests on every read.
func TestLegacyInputWarningsAreLoggedOnce(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "stale-compat-warning", map[string]any{
		"id": "stale-compat-warning", "name": "Stale", "version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": []string{
			"storage:kv", "events:subscribe", "commands:register",
			"tools:expose", "pets:contribute",
		},
		"contributes": map[string]any{
			"settingsPanels": []any{map[string]any{"id": "x", "title": "X"}},
		},
	}, "bundle")
	// A segment holding nothing this host ever read is not worth a
	// warning: the breadcrumb has to stay rare enough to mean something.
	for id, contributes := range map[string]any{
		"empty-contributes":     map[string]any{},
		"list-contributes":      []any{},
		"unrelated-contributes": map[string]any{"somethingElse": true},
	} {
		writePlugin(t, root, id, map[string]any{
			"id": id, "name": id, "version": "0.1.0",
			"entry":       "dist/index.js",
			"permissions": []string{},
			"contributes": contributes,
		}, "bundle")
	}

	recorder := logcapture.Install(t)
	s := NewStore(root)
	for i := 0; i < 2; i++ {
		list, err := s.List()
		if err != nil {
			t.Fatalf("List %d: %v", i, err)
		}
		if len(list) != 4 {
			t.Fatalf("List %d returned %d plugins", i, len(list))
		}
		// The manifest keeps loading, on the canonical vocabulary.
		for _, sum := range list {
			if sum.ID != "stale-compat-warning" {
				continue
			}
			if got := sum.Permissions; !slices.Equal(
				got, []string{"storage:kv", "tools:provide"},
			) {
				t.Fatalf("List %d permissions = %v, want [storage:kv tools:provide]",
					i, got)
			}
		}
	}

	bodies := recorder.Bodies()
	count := func(fragment string) int {
		n := 0
		for _, body := range bodies {
			if strings.Contains(body, fragment) {
				n++
			}
		}
		return n
	}
	if got := count("retired permission"); got != 3 {
		t.Errorf("retired-permission lines after two scans = %d, want 3 "+
			"(one per retired name, none repeated): %v", got, bodies)
	}
	if got := count("which the host no longer reads"); got != 1 {
		t.Errorf("contributes breadcrumb fired %d times after two scans, "+
			"want 1 (and only for the plugin that declares a panel): %v",
			got, bodies)
	}
	if got := count("contributes.settingsPanels"); got != 1 {
		t.Errorf("the breadcrumb does not name the key it dropped: %v", bodies)
	}
}

// TestHelloPluginInstalls runs the reference plugin the plugin-creator
// skill points at through the same inspect/install path a user install
// takes. It is the one plugin in this repository that has to keep
// loading, and nothing else in the suite opens plugins/hello.
func TestHelloPluginInstalls(t *testing.T) {
	src := filepath.Join("..", "..", "..", "plugins", "hello")
	if _, err := os.Stat(filepath.Join(src, "plugin.json")); err != nil {
		t.Fatalf("reference plugin is missing: %v", err)
	}

	s := NewStore(t.TempDir())
	sum, err := s.Inspect(src)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if sum.ID != "hello" || sum.Version == "" {
		t.Fatalf("summary = %+v", sum)
	}
	if got := sum.Permissions; !slices.Equal(got, []string{"storage:kv", "skills:provide"}) {
		t.Fatalf("permissions = %v, want the canonical [storage:kv skills:provide]", got)
	}
	if !sum.HasSkills {
		t.Error("hello declares skills but the summary does not report them")
	}
	if sum.Error != "" {
		t.Fatalf("inspect error = %q", sum.Error)
	}

	if _, err := s.Install(src); err != nil {
		t.Fatalf("Install: %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "hello" || !list[0].Enabled {
		t.Fatalf("installed list = %+v", list)
	}
	if _, err := s.Bundle("hello"); err != nil {
		t.Fatalf("bundle after install: %v", err)
	}
	dir, builtin, err := s.Dir("hello")
	if err != nil || builtin {
		t.Fatalf("Dir: builtin=%v err=%v", builtin, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills")); err != nil {
		t.Fatalf("installed plugin has no skills directory: %v", err)
	}
}
