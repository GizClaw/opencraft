package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newWriteService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return NewService(Options{
		WorkBase: root,
		UserDir:  t.TempDir(),
		Enabled:  true,
	})
}

func TestCreateModifyDeleteRoundTrip(t *testing.T) {
	svc := newWriteService(t)

	// Create in the repo scope and verify it is discoverable at once.
	path, err := svc.Create(
		"qa-checks",
		SkillDocument{
			Description: "run the QA checklist",
			Body:        "## Steps\n1. Build.\n2. Test.\n",
			Files: map[string]string{
				"scripts/run.py":      "#!/usr/bin/env python3\nprint('ok')\n",
				"references/notes.md": "# Notes\nKeep them short.\n",
			},
			Executable: []string{"scripts/run.py"},
		},
		ScopeRepo,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(
		svc.opts.WorkBase, ".agents", "skills", "qa-checks", "SKILL.md",
	); path != want {
		t.Fatalf("create path = %s, want %s", path, want)
	}
	if _, _, err := svc.ReadFull("qa-checks"); err != nil {
		t.Fatalf("created skill not discoverable: %v", err)
	}
	// Supporting files land with the right content and mode.
	script := filepath.Join(
		svc.opts.WorkBase, ".agents", "skills", "qa-checks", "scripts", "run.py",
	)
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("script missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("script not executable: %v", info.Mode())
	}
	if data, err := os.ReadFile(filepath.Join(
		svc.opts.WorkBase, ".agents", "skills", "qa-checks", "references", "notes.md",
	)); err != nil || !strings.Contains(string(data), "Keep them short") {
		t.Fatalf("reference file = %q, %v", data, err)
	}

	// Duplicate name is refused.
	if _, err := svc.Create(
		"qa-checks", SkillDocument{Description: "dup", Body: "body"}, ScopeRepo,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate create error = %v", err)
	}

	// Invalid names are refused.
	if _, err := svc.Create(
		"Bad Name", SkillDocument{Description: "x", Body: "body"}, ScopeRepo,
	); err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("invalid name error = %v", err)
	}

	// Escaping file paths are refused.
	if _, err := svc.Create(
		"evil",
		SkillDocument{
			Description: "x",
			Body:        "body",
			Files:       map[string]string{"../evil.txt": "boom"},
		},
		ScopeRepo,
	); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("traversal error = %v", err)
	}
	if _, err := svc.Create(
		"evil2",
		SkillDocument{
			Description: "x",
			Body:        "body",
			Files:       map[string]string{"SKILL.md": "hijack"},
		},
		ScopeRepo,
	); err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("SKILL.md hijack error = %v", err)
	}

	// Modify replaces the body and keeps the description when blank.
	if _, err := svc.Modify(
		"qa-checks",
		SkillDocument{
			Body: "## Steps\n1. Build.\n2. Test.\n3. Ship.\n",
			Files: map[string]string{
				"scripts/run.py": "#!/usr/bin/env python3\nprint('v2')\n",
			},
		},
		ScopeRepo,
	); err != nil {
		t.Fatal(err)
	}
	meta, body, err := svc.ReadFull("qa-checks")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "run the QA checklist" {
		t.Fatalf("description changed: %q", meta.Description)
	}
	if !strings.Contains(body, "3. Ship.") {
		t.Fatalf("body not updated: %q", body)
	}
	// Upserted file replaced, untouched file preserved.
	if data, err := os.ReadFile(script); err != nil ||
		!strings.Contains(string(data), "v2") {
		t.Fatalf("modified script = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(
		svc.opts.WorkBase, ".agents", "skills", "qa-checks", "references", "notes.md",
	)); err != nil {
		t.Fatalf("unlisted reference file removed: %v", err)
	}

	// Modify of a missing skill is refused.
	if _, err := svc.Modify(
		"nope", SkillDocument{Body: "body"}, ScopeRepo,
	); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing modify error = %v", err)
	}

	// Delete removes the skill directory and reloads the registry.
	if err := svc.Delete(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReadFull("qa-checks"); err == nil {
		t.Fatal("deleted skill still discoverable")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("skill directory still present: %v", err)
	}

	// Builtin and out-of-root paths are refused.
	if err := svc.Delete("builtin://plan/SKILL.md"); err == nil {
		t.Fatal("builtin delete must fail")
	}
	outside := filepath.Join(t.TempDir(), "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(outside); err == nil ||
		!strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside delete error = %v", err)
	}
	if err := svc.Delete(filepath.Join(
		svc.opts.WorkBase, ".agents", "skills", "README.md",
	)); err == nil {
		t.Fatal("non-SKILL.md delete must fail")
	}
}

func TestCreateUserScope(t *testing.T) {
	svc := newWriteService(t)
	path, err := svc.Create(
		"notes",
		SkillDocument{
			Description: "take notes",
			Body:        "## Notes\nKeep them short.\n",
		},
		ScopeUser,
	)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(path, filepath.Join(home, ".agents", "skills")) {
		t.Fatalf("user-scope path = %s", path)
	}
	if err := svc.Delete(path); err != nil {
		t.Fatal(err)
	}
}

func TestPatchSkillPartialEdit(t *testing.T) {
	svc := newWriteService(t)
	if _, err := svc.Create(
		"qa-checks",
		SkillDocument{
			Description: "run the QA checklist",
			Body:        "## Steps\n1. Build.\n2. Test.\n",
			Files: map[string]string{
				"scripts/run.py": "#!/usr/bin/env python3\nprint('v1')\n",
			},
			Executable: []string{"scripts/run.py"},
		},
		ScopeRepo,
	); err != nil {
		t.Fatal(err)
	}

	paths, err := svc.Patch("qa-checks", `*** Begin Patch
*** Update File: SKILL.md
@@
-2. Test.
+2. Test everything.
*** Update File: scripts/run.py
@@
-print('v1')
+print('v2')
*** End Patch
`, ScopeRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("patched paths = %+v", paths)
	}
	_, body, err := svc.ReadFull("qa-checks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "2. Test everything.") {
		t.Fatalf("SKILL.md not patched: %q", body)
	}
	script := filepath.Join(
		svc.opts.WorkBase, ".agents", "skills", "qa-checks", "scripts", "run.py",
	)
	if data, err := os.ReadFile(script); err != nil ||
		!strings.Contains(string(data), "v2") {
		t.Fatalf("script not patched: %q, %v", data, err)
	}
	if info, err := os.Stat(script); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v", err)
	}

	// A patch that breaks the SKILL.md frontmatter leaves the skill
	// untouched (staged copy is discarded).
	if _, err := svc.Patch("qa-checks", `*** Begin Patch
*** Update File: SKILL.md
@@
-description: run the QA checklist
+description:
*** End Patch
`, ScopeRepo); err == nil {
		t.Fatal("broken frontmatter must be rejected")
	}
	_, body, err = svc.ReadFull("qa-checks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "2. Test everything.") {
		t.Fatalf("failed patch modified the skill: %q", body)
	}

	// Missing skill and escaping paths are refused.
	if _, err := svc.Patch("nope", "*** Begin Patch\n*** End Patch\n", ScopeRepo); err == nil {
		t.Fatal("missing skill patch must fail")
	}
	if _, err := svc.Patch(
		"qa-checks",
		"*** Begin Patch\n*** Update File: ../x\n@@\n-a\n+b\n*** End Patch\n",
		ScopeRepo,
	); err == nil {
		t.Fatal("escaping patch must fail")
	}
}
