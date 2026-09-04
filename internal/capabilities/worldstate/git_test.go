package worldstate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-qm", "init")
}

func TestGitSection(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := New(Options{WorkBase: root})
	sec := svc.gitSection(context.Background())
	if sec.ID != "git" || sec.Role != "system" {
		t.Fatalf("section = %+v, want git/system", sec)
	}
	for _, want := range []string{
		"## Git state",
		"branch:",
		"M a.go",
		"?? b.go",
		"diffstat:",
		"diff:",
	} {
		if !strings.Contains(sec.Text, want) {
			t.Errorf("git section missing %q:\n%s", want, sec.Text)
		}
	}
}

func TestGitSectionNonRepo(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	sec := svc.gitSection(context.Background())
	if sec.Text != "" {
		t.Fatalf("non-repo git section = %q, want empty", sec.Text)
	}
}

func TestGitSectionLargeDiffOmitted(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	// Track a file first, then blow the 6 KiB diff budget by rewriting
	// it with thousands of lines (untracked files never appear in
	// `git diff HEAD`, so they cannot exercise the omission path).
	if err := os.WriteFile(filepath.Join(root, "big.txt"),
		[]byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("git", "-C", root, "add", "big.txt")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	run = exec.Command("git", "-C", root, "commit", "-qm", "add big")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("line with enough text to blow the diff budget\n")
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"),
		[]byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := New(Options{WorkBase: root})
	sec := svc.gitSection(context.Background())
	if !strings.Contains(sec.Text, "diff omitted (too large)") {
		t.Fatalf("large diff should be omitted with a hint:\n%s", sec.Text)
	}
	if strings.Contains(sec.Text, "diff:\n@@") {
		t.Fatalf("large diff must not be injected:\n%s", sec.Text)
	}
	if len(sec.Text) > gitStatusBudget+gitDiffStatBudget+2048 {
		t.Fatalf("git section too large: %d bytes", len(sec.Text))
	}
}

func TestCapStatusLines(t *testing.T) {
	var lines []string
	for i := 0; i < 120; i++ {
		lines = append(lines, "?? file_"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	in := strings.Join(lines, "\n")
	got := capStatusLines(in, 100)
	if strings.Count(got, "\n") != 100 {
		t.Fatalf("capStatusLines kept %d lines, want 100", strings.Count(got, "\n"))
	}
	if !strings.Contains(got, "…(+20 more)") {
		t.Fatalf("capStatusLines missing marker: %q", got)
	}
	if capStatusLines("a\nb", 100) != "a\nb" {
		t.Fatal("short input must pass through untouched")
	}
}
