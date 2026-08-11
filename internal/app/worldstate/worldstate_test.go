package worldstate

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAgentsRootToCwdWithOverride(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"), "")
	sub := filepath.Join(root, "a", "b")
	write(t, filepath.Join(root, "AGENTS.md"), "root doc")
	write(t, filepath.Join(sub, "AGENTS.md"), "sub doc")
	write(t, filepath.Join(sub, "AGENTS.override.md"), "override doc")

	s := New(Options{WorkBase: sub})
	got, err := s.discoverAgents()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "root doc") {
		t.Fatalf("missing root doc: %q", got)
	}
	if contains(got, "sub doc") {
		t.Fatalf("sub doc must be overridden: %q", got)
	}
	if !contains(got, "override doc") {
		t.Fatalf("missing override doc: %q", got)
	}
}

func TestDiscoverAgentsFallsBackToCwd(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "only here")
	s := New(Options{WorkBase: dir})
	got, err := s.discoverAgents()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "only here") {
		t.Fatalf("got %q", got)
	}
}

func TestDiscoverAgentsViaWorkspace(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"), "")
	write(t, filepath.Join(root, "AGENTS.md"), "workspace doc")
	ws, err := NewLocalWorkspaceForTest(root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{WorkBase: root, Workspace: ws})
	got, err := s.discoverAgents()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "workspace doc") {
		t.Fatalf("got %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
