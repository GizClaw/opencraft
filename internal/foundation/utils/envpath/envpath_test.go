package envpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// list joins directories the way a PATH value does.
func list(dirs ...string) string {
	return strings.Join(dirs, string(filepath.ListSeparator))
}

// entries splits a resolved PATH value back into directories.
func entries(path string) []string {
	return filepath.SplitList(path)
}

// sourceOf returns the source recorded for one directory, or "" when the
// directory is not on the resolved PATH.
func sourceOf(plan Plan, dir string) string {
	for _, segment := range plan.Segments {
		if segment.Dir == dir {
			return segment.Source
		}
	}
	return ""
}

func TestResolveOrdersPrependInheritedThenCandidates(t *testing.T) {
	prepended := t.TempDir()
	inheritedFirst := t.TempDir()
	inheritedSecond := t.TempDir()
	candidate := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-installed")

	plan := Resolve(
		list(inheritedFirst, inheritedSecond, prepended),
		[]string{prepended},
		[]string{candidate, missing},
	)

	want := []string{
		prepended, inheritedFirst, inheritedSecond, candidate,
	}
	if got := entries(plan.Path); !equalStrings(got, want) {
		t.Fatalf("resolved PATH = %v, want %v", got, want)
	}
	// The prepend entry also sat on the inherited PATH; it must be
	// promoted to the front instead of appearing twice.
	if got := sourceOf(plan, prepended); got != SourcePrepend {
		t.Fatalf("source of %s = %q, want %q", prepended, got, SourcePrepend)
	}
	if got := sourceOf(plan, inheritedFirst); got != SourceInherited {
		t.Fatalf("source of %s = %q, want %q", inheritedFirst, got, SourceInherited)
	}
	if got := sourceOf(plan, candidate); got != SourceCandidate {
		t.Fatalf("source of %s = %q, want %q", candidate, got, SourceCandidate)
	}
	if !equalStrings(plan.Missing, []string{missing}) {
		t.Fatalf("missing = %v, want [%s]", plan.Missing, missing)
	}
	if len(plan.Rejected) != 0 {
		t.Fatalf("rejected = %v, want none", plan.Rejected)
	}
}

func TestResolveRejectsRelativeAndFilePrependEntries(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	plan := Resolve(list(dir), []string{"relative/bin", "", file}, nil)

	if !equalStrings(plan.Rejected, []string{"relative/bin", file}) {
		t.Fatalf("rejected = %v, want the relative entry and the file", plan.Rejected)
	}
	for _, segment := range plan.Segments {
		if segment.Source == SourcePrepend {
			t.Fatalf("rejected entry %s was applied", segment.Dir)
		}
	}
}

func TestResolveKeepsMissingPrependEntry(t *testing.T) {
	inherited := t.TempDir()
	abs := filepath.Join(t.TempDir(), "installed-later")
	plan := Resolve(inherited, []string{abs}, nil)

	if got := entries(plan.Path); !equalStrings(got, []string{abs, inherited}) {
		t.Fatalf("resolved PATH = %v, want the missing prepend entry kept", got)
	}
	segment := plan.Segments[0]
	if segment.Source != SourcePrepend || segment.Present {
		t.Fatalf("segment = %+v, want a prepend entry reported as absent", segment)
	}
}

func TestResolveDropsEmptyInheritedEntries(t *testing.T) {
	dir := t.TempDir()
	plan := Resolve(
		string(filepath.ListSeparator)+dir+string(filepath.ListSeparator)+
			string(filepath.ListSeparator)+"/usr/bin"+string(filepath.ListSeparator),
		nil, nil,
	)
	for _, segment := range plan.Segments {
		if segment.Dir == "" || segment.Dir == "." {
			t.Fatalf("empty PATH entry survived as %q", segment.Dir)
		}
	}
	if got := entries(plan.Path); !equalStrings(got, []string{dir, "/usr/bin"}) {
		t.Fatalf("resolved PATH = %v, want [%s /usr/bin]", got, dir)
	}
}

func TestInstallWritesPathOnce(t *testing.T) {
	inherited := t.TempDir()
	candidate := t.TempDir()
	t.Setenv("PATH", inherited)

	first, err := Install(Options{Candidates: []string{candidate}})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !first.Changed || first.Inherited != inherited {
		t.Fatalf("first Install = %+v, want a change from %s", first, inherited)
	}
	if got := os.Getenv("PATH"); !equalStrings(entries(got), []string{inherited, candidate}) {
		t.Fatalf("process PATH = %q, want inherited then candidate", got)
	}

	second, err := Install(Options{Candidates: []string{candidate}})
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if second.Changed {
		t.Fatalf("second Install = %+v, want an unchanged PATH", second)
	}
	if got := os.Getenv("PATH"); !equalStrings(entries(got), []string{inherited, candidate}) {
		t.Fatalf("process PATH after reinstall = %q, want it unchanged", got)
	}
}

func TestCandidates(t *testing.T) {
	home := "/home/someone"
	darwin := Candidates("darwin", home)
	if len(darwin) == 0 || darwin[0] != "/opt/homebrew/bin" {
		t.Fatalf("darwin candidates = %v, want Homebrew first", darwin)
	}
	// A candidate that cannot exist on the platform is noise in the
	// diagnostics view: an absent candidate is reported as a signal.
	for _, unwanted := range []string{"/snap/bin", "/home/linuxbrew/.linuxbrew/bin"} {
		if contains(darwin, unwanted) {
			t.Fatalf("darwin candidates %v contain %s", darwin, unwanted)
		}
	}
	linux := Candidates("linux", home)
	if contains(linux, "/opt/homebrew/bin") || !contains(linux, "/snap/bin") {
		t.Fatalf("linux candidates = %v, want snap and no Homebrew", linux)
	}
	if got := Candidates("windows", home); len(got) != 0 {
		t.Fatalf("windows candidates = %v, want none", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		seen := make(map[string]bool)
		for _, candidate := range Candidates(goos, home) {
			if !filepath.IsAbs(candidate) {
				t.Fatalf("%s candidate %q is not absolute", goos, candidate)
			}
			if seen[candidate] {
				t.Fatalf("%s candidate %q is duplicated", goos, candidate)
			}
			seen[candidate] = true
			if strings.Contains(candidate, "fnm") || strings.Contains(candidate, "nvm") {
				t.Fatalf("%s candidate %q guesses a version manager", goos, candidate)
			}
		}
	}
	// A launch outside a resolvable home directory must not produce
	// relative entries.
	for _, candidate := range Candidates("linux", "") {
		if !filepath.IsAbs(candidate) {
			t.Fatalf("home-less candidate %q is not absolute", candidate)
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
