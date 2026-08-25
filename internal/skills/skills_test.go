package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates one SKILL.md under the scan root with the given
// frontmatter.
func writeSkill(t *testing.T, scanRoot, name, frontmatter string) string {
	t.Helper()
	dir := filepath.Join(scanRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	body := "---\n" + frontmatter + "---\n\n# Instructions\nDo the thing.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverRepoLevelsAndUserDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	workBase := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(workBase, 0o755); err != nil {
		t.Fatal(err)
	}
	userDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeSkill(t, filepath.Join(root, ".agents", "skills"), "alpha",
		"name: alpha\ndescription: alpha skill at repo root\n")
	writeSkill(t, filepath.Join(workBase, ".agents", "skills"), "alpha",
		"name: alpha\ndescription: alpha skill at cwd\n")
	writeSkill(t, filepath.Join(userDir, "skills"), "beta",
		"name: beta\ndescription: user-level beta\n")
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "gamma",
		"name: gamma\ndescription: home-level gamma\n")
	// Duplicate name across repo and user scopes: cwd must win (D3).
	writeSkill(t, filepath.Join(workBase, ".agents", "skills"), "dup",
		"name: dup\ndescription: cwd-level dup\n")
	writeSkill(t, filepath.Join(userDir, "skills"), "dup",
		"name: dup\ndescription: user-level dup\n")

	out := Discover(workBase, userDir, nil, nil)
	if len(out.Skills) != 6 {
		t.Fatalf("Discover() = %d skills, want 6: %+v", len(out.Skills), out.Skills)
	}

	svc := NewService(Options{WorkBase: workBase, UserDir: userDir, Enabled: true})
	got, ok := svc.ByName("alpha")
	if !ok {
		t.Fatal("ByName(alpha) not found")
	}
	// Nearest scope wins: cwd layer beats repo root.
	if !strings.Contains(got.Path, filepath.Join("sub", "dir")) {
		t.Fatalf("ByName(alpha) = %q, want cwd-level path", got.Path)
	}
	dup, ok := svc.ByName("dup")
	if !ok || !strings.Contains(dup.Path, filepath.Join("sub", "dir")) {
		t.Fatalf("ByName(dup) = %q, want cwd-level dup to beat user-level", dup.Path)
	}
	if len(svc.List()) != 10 { // 6 discovered + 4 built-ins
		t.Fatalf("List() = %d, want 10", len(svc.List()))
	}
}

func TestParseFileFallbackAndWarnings(t *testing.T) {
	root := t.TempDir()
	// Invalid name (uppercase + underscore): falls back to the
	// slugified directory name and keeps loading.
	dir := filepath.Join(root, "My_Skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(
		"---\nname: My_Skill\ndescription: bad name\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile with invalid name should not fail: %v", err)
	}
	if res.Metadata.Name != "my-skill" {
		t.Fatalf("fallback name = %q, want my-skill", res.Metadata.Name)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("invalid name should produce a warning")
	}

	// Missing description is fatal.
	bad := writeSkill(t, root, "nod", "name: nod\n")
	if _, err := ParseFile(bad); err == nil {
		t.Fatal("missing description should fail")
	}

	// Missing frontmatter is fatal.
	noFm := filepath.Join(root, "nofm")
	if err := os.MkdirAll(noFm, 0o755); err != nil {
		t.Fatal(err)
	}
	noFmPath := filepath.Join(noFm, "SKILL.md")
	if err := os.WriteFile(noFmPath, []byte("no frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(noFmPath); err == nil {
		t.Fatal("missing frontmatter should fail")
	}
}

func TestRankAndMention(t *testing.T) {
	root := t.TempDir()
	scanRoot := filepath.Join(root, ".agents", "skills")
	writeSkill(t, scanRoot, "review",
		"name: review\ndescription: review code and docs for quality\n")
	writeSkill(t, scanRoot, "plan",
		"name: plan\ndescription: build execution plans\n")
	writeSkill(t, scanRoot, "search",
		"name: search\ndescription: search the workspace\n")
	svc := NewService(Options{WorkBase: root, Enabled: true, TopN: 2})

	ranked := svc.Rank("review the docs", 2, 0)
	if len(ranked) == 0 || ranked[0].Name != "review" {
		t.Fatalf("Rank(review) = %+v, want review first", ranked)
	}
	// TopN caps the result.
	if len(ranked) > 2 {
		t.Fatalf("Rank topN = %d, want <= 2", len(ranked))
	}
	// No match -> empty.
	if got := svc.Rank("zzzzqqqq", 5, 0); len(got) != 0 {
		t.Fatalf("Rank(no match) = %+v, want empty", got)
	}
	// Empty query -> empty (no injection when nothing relevant).
	if got := svc.Rank("", 5, 0); len(got) != 0 {
		t.Fatalf("Rank(empty) = %+v, want empty", got)
	}

	mentioned := svc.Mentioned("use $plan and $review for this")
	if len(mentioned) != 2 {
		t.Fatalf("Mentioned = %+v, want plan and review", mentioned)
	}
	// Duplicate mentions are de-duplicated; unknown names ignored.
	if got := svc.Mentioned("$plan $plan $nope"); len(got) != 1 {
		t.Fatalf("Mentioned dup = %+v, want one", got)
	}
}

func TestRankMinScoreThreshold(t *testing.T) {
	root := t.TempDir()
	scan := filepath.Join(root, ".agents", "skills")
	writeSkill(t, scan, "review", "name: review\ndescription: review code and docs\n")
	svc := NewService(Options{WorkBase: root, Enabled: true})

	scored := svc.RankScored("review the code", 5, 0)
	if len(scored) == 0 {
		t.Fatal("matching query must rank at least one skill")
	}
	top := scored[0].Score
	if len(svc.Rank("review the code", 5, top)) < 1 {
		t.Fatalf("threshold %v must keep top matches", top)
	}
	if len(svc.Rank("review the code", 5, top+1)) != 0 {
		t.Fatalf("threshold %v must filter everything", top+1)
	}
}

func TestReadFullAndRender(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "review",
		"name: review\ndescription: review code\n")
	svc := NewService(Options{WorkBase: root, Enabled: true})
	sk, body, err := svc.ReadFull("review")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Do the thing.") {
		t.Fatalf("ReadFull body = %q, want instructions", body)
	}
	if _, _, err := svc.ReadFull("missing"); err == nil {
		t.Fatal("ReadFull(missing) should fail")
	}

	if got := RenderSection(nil); got != "" {
		t.Fatalf("RenderSection(nil) = %q, want empty", got)
	}
	sec := RenderSection([]SkillMetadata{sk})
	if !strings.Contains(sec, "## Skills") ||
		!strings.Contains(sec, "review") ||
		strings.Contains(sec, "Do the thing.") {
		t.Fatalf("RenderSection = %q, want metadata only (no body)", sec)
	}
}

func TestDisabledAndHiddenSkip(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "visible",
		"name: visible\ndescription: visible skill\n")
	hidden := filepath.Join(root, ".agents", "skills", ".hidden")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "SKILL.md"),
		[]byte("---\nname: hidden\ndescription: hidden\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(Options{WorkBase: root, Enabled: true})
	for _, sk := range svc.List() {
		if sk.Name == "visible" {
			return
		}
		if sk.Name == "hidden" {
			t.Fatalf("hidden skill must be skipped: %+v", svc.List())
		}
	}
	t.Fatalf("visible skill missing: %+v", svc.List())

	disabled := NewService(Options{WorkBase: root, Enabled: false})
	if len(disabled.List()) != 0 || disabled.Enabled() {
		t.Fatal("disabled service must be empty")
	}
}

func TestDisabledList(t *testing.T) {
	root := t.TempDir()
	scan := filepath.Join(root, ".agents", "skills")
	writeSkill(t, scan, "keep", "name: keep\ndescription: keep me\n")
	path := writeSkill(t, scan, "drop", "name: drop\ndescription: drop me\n")

	svc := NewService(Options{
		WorkBase: root,
		Enabled:  true,
		Disabled: []string{"drop", path},
	})
	for _, sk := range svc.List() {
		if sk.Name == "drop" {
			t.Fatalf("disabled skill drop must be filtered: %+v", svc.List())
		}
		if sk.Name == "keep" {
			return
		}
	}
	t.Fatalf("keep must survive the disabled filter: %+v", svc.List())
}

func TestFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(t.TempDir(), "linked")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, real, "linked-skill",
		"name: linked-skill\ndescription: reached via symlink\n")
	scan := filepath.Join(root, ".agents", "skills")
	if err := os.MkdirAll(scan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(scan, "linked")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	svc := NewService(Options{WorkBase: root, Enabled: true})
	found := false
	for _, sk := range svc.List() {
		if sk.Name == "linked-skill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("symlinked skill must be discovered: %+v", svc.List())
	}
}

func TestBuiltinEmbedded(t *testing.T) {
	svc := NewService(Options{WorkBase: t.TempDir(), Enabled: true})
	for _, name := range []string{"plan", "code-review", "skill-creator"} {
		sk, ok := svc.ByName(name)
		if !ok {
			t.Fatalf("builtin %s missing", name)
		}
		if sk.Scope != "builtin" || !strings.HasPrefix(sk.Path, "builtin://") {
			t.Fatalf("builtin %s metadata = %+v", name, sk)
		}
		_, body, err := svc.ReadFull(name)
		if err != nil || !strings.Contains(body, "#") {
			t.Fatalf("builtin %s body: %q, %v", name, body, err)
		}
	}
}

func TestStageCopiesSkill(t *testing.T) {
	root := t.TempDir()
	scan := filepath.Join(root, ".agents", "skills", "tool")
	writeSkill(t, filepath.Join(root, ".agents", "skills"), "tool",
		"name: tool\ndescription: packaged tool skill\n")
	scripts := filepath.Join(scan, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "run.sh"),
		[]byte("#!/bin/sh\necho staged\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(scan, "leak")); err == nil {
		defer func() { _ = os.Remove(filepath.Join(scan, "leak")) }()
	}

	svc := NewService(Options{WorkBase: root, Enabled: true})
	sk, ok := svc.ByName("tool")
	if !ok {
		t.Fatal("tool skill not discovered")
	}
	dst := t.TempDir()
	staged, err := svc.Stage(sk, dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"SKILL.md", "scripts/run.sh",
	} {
		if _, err := os.Stat(filepath.Join(staged, rel)); err != nil {
			t.Fatalf("staged %s missing: %v", rel, err)
		}
	}
	// The staged copy keeps the source executable bit so the sandbox
	// can fork/exec skill scripts.
	info, err := os.Stat(filepath.Join(staged, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("staged run.sh mode = %v, want executable", info.Mode())
	}
	// Symlink targets must NOT be followed out of the staged copy.
	if _, err := os.Stat(filepath.Join(staged, "leak")); err == nil {
		t.Fatal("symlink must not be copied into the staged skill")
	}
}

func TestInstallFromLocalRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(
		"---\nname: installed-skill\ndescription: freshly installed\n---\n\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("add", ".")
	git("commit", "-m", "init")

	workBase := t.TempDir()
	svc := NewService(Options{WorkBase: workBase, Enabled: true})
	dst, err := svc.Install(src, ScopeRepo, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}
	if _, ok := svc.ByName("installed-skill"); !ok {
		t.Fatalf("installed skill not in registry after reload: %+v",
			svc.List())
	}

	// A repo without a valid SKILL.md fails and leaves no directory.
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "README.md"),
		[]byte("no skill here"), 0o644); err != nil {
		t.Fatal(err)
	}
	git2 := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = bad
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git2("init", "-b", "main")
	git2("add", ".")
	git2("commit", "-m", "init")
	before := len(svc.List())
	if _, err := svc.Install(bad, ScopeRepo, ""); err == nil {
		t.Fatal("repo without SKILL.md must fail to install")
	}
	if len(svc.List()) != before {
		t.Fatal("failed install must not change the registry")
	}
}

func TestInstallSubpathFromRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src := t.TempDir()
	sub := filepath.Join(src, "skills", "flowcraft-config")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(
		"---\nname: flowcraft-config\ndescription: flowcraft config skill\n---\n\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "README.md"),
		[]byte("repo junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("add", ".")
	git("commit", "-m", "init")

	workBase := t.TempDir()
	svc := NewService(Options{WorkBase: workBase, Enabled: true})
	dst, err := svc.Install(src, ScopeRepo, "skills/flowcraft-config")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dst) != "flowcraft-config" {
		t.Fatalf("subpath install dir = %q, want flowcraft-config", dst)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatalf("subpath SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "README.md")); err == nil {
		t.Fatal("repo junk must not leak into the installed skill")
	}
	if _, ok := svc.ByName("flowcraft-config"); !ok {
		t.Fatal("subpath-installed skill not in registry")
	}
}

func TestInstallSubpathTraversalRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(
		"---\nname: traversalskill\ndescription: traversal test\n---\n\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("add", ".")
	git("commit", "-m", "init")

	workBase := t.TempDir()
	svc := NewService(Options{WorkBase: workBase, Enabled: true})
	for _, subpath := range []string{
		"..",
		"../escape",
		"../../escape",
		"/etc",
	} {
		if _, err := svc.Install(src, ScopeRepo, subpath); err == nil {
			t.Fatalf("Install(subpath=%q) accepted a traversal", subpath)
		}
	}
	// A repo symlink pointing outside the clone must also be rejected.
	if err := os.Symlink(workBase, filepath.Join(src, "evil")); err != nil {
		t.Fatal(err)
	}
	git("add", "evil")
	git("commit", "-m", "add symlink")
	if _, err := svc.Install(src, ScopeRepo, "evil"); err == nil {
		t.Fatal("Install(subpath=symlink-outside) accepted")
	}
}
