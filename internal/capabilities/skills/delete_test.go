package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestDeleteRemovesOneSkillDirectory is the positive control for the
// root guard: deleting a skill directory inside a writable root keeps
// working.
func TestDeleteRemovesOneSkillDirectory(t *testing.T) {
	svc := newWriteService(t)
	root := writeRoot(t)
	skillPath := writeSkill(t, root, "alpha",
		"name: alpha\ndescription: alpha skill\n")

	if err := svc.Delete(skillPath); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(skillPath)); !os.IsNotExist(err) {
		t.Fatalf("skill directory survived delete: %v", err)
	}
}

// TestDeleteRefusesRootLevelSkill pins the guard: a SKILL.md sitting
// directly in a scanned root is listed as a skill, and deleting it
// would RemoveAll the root — every sibling skill with it.
func TestDeleteRefusesRootLevelSkill(t *testing.T) {
	svc := newWriteService(t)
	root := writeRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rootDoc := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(rootDoc,
		[]byte("---\nname: rooted\ndescription: root-level skill\n---\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	sibling := writeSkill(t, root, "other",
		"name: other\ndescription: sibling skill\n")

	if err := svc.Delete(rootDoc); err == nil {
		t.Fatal("deleting a root-level SKILL.md must be refused")
	}
	// Refused means refused: nothing under the root moved.
	if _, err := os.Stat(rootDoc); err != nil {
		t.Fatalf("root-level SKILL.md vanished after refused delete: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling skill vanished after refused delete: %v", err)
	}
}

// TestDeleteRefusesExtraRootSkills pins the writable-root scope:
// plugin-provided roots are discovered (and listed) but never deletable
// through this service — their lifecycle belongs to the plugin Store,
// and the settings UI does not offer removal for them either.
func TestDeleteRefusesExtraRootSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	extra := filepath.Join(home, "extra-skills")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeSkill(t, extra, "plugin-skill",
		"name: plugin-skill\ndescription: plugin skill\n")

	svc := NewService(context.Background(), Options{
		Enabled:    true,
		UserDir:    filepath.Join(home, "user"),
		ExtraRoots: []string{extra},
	})

	if err := svc.Delete(path); err == nil {
		t.Fatal("deleting a plugin (extra-root) skill must be refused")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plugin skill vanished after refused delete: %v", err)
	}
}

// TestDeleteRefusesNonRegularDocument pins the last input the shared
// decision reads: a directory named SKILL.md resolves inside a writable
// root, so containment alone would accept it, but there is no document
// to delete. Delete and Deletable — which feeds the settings delete
// button — have to answer that identically.
func TestDeleteRefusesNonRegularDocument(t *testing.T) {
	svc := newWriteService(t)
	root := writeRoot(t)
	dir := filepath.Join(root, "notafile", "SKILL.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if svc.Deletable(dir) {
		t.Fatal("Deletable accepted a directory named SKILL.md")
	}
	if err := svc.Delete(dir); err == nil {
		t.Fatal("deleting a directory named SKILL.md must be refused")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory vanished after refused delete: %v", err)
	}
}

// TestDeleteRefusesSymlinkEscape pins the resolved-path containment
// check: a link planted in a skill root must not turn Delete into a
// recursive removal of its target outside the writable roots.
func TestDeleteRefusesSymlinkEscape(t *testing.T) {
	svc := newWriteService(t)
	root := writeRoot(t)
	outside := t.TempDir()
	victim := writeSkill(t, outside, "victim",
		"name: victim\ndescription: victim skill\n")
	keep := filepath.Join(outside, "victim", "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	holder := filepath.Join(root, "holder")
	if err := os.MkdirAll(holder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(holder, "escape")); err != nil {
		t.Fatal(err)
	}

	err := svc.Delete(filepath.Join(holder, "escape", "victim", "SKILL.md"))
	if err == nil {
		t.Fatal("deleting through a symlink that leaves the roots must be refused")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("victim skill vanished after refused delete: %v", statErr)
	}
	if _, statErr := os.Stat(keep); statErr != nil {
		t.Fatalf("victim file vanished after refused delete: %v", statErr)
	}
}

// TestDeleteRefusesSkillRootThroughSymlink pins the root-equality guard
// against its symlinked spellings: a link to the root (or to its
// parent) must not let Delete RemoveAll the whole root through a path
// the lexical comparison reads as a child.
func TestDeleteRefusesSkillRootThroughSymlink(t *testing.T) {
	svc := newWriteService(t)
	root := writeRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rootDoc := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(rootDoc,
		[]byte("---\nname: rooted\ndescription: root-level skill\n---\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	sibling := writeSkill(t, root, "other",
		"name: other\ndescription: sibling skill\n")
	// A link pointing at the root itself, and one pointing at its
	// parent with the root's name spelled through it.
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	up := filepath.Join(root, "up")
	if err := os.Symlink(filepath.Dir(root), up); err != nil {
		t.Fatal(err)
	}

	spellings := []string{
		filepath.Join(root, "loop", "SKILL.md"),
		filepath.Join(up, filepath.Base(root), "SKILL.md"),
	}
	for _, spelling := range spellings {
		if err := svc.Delete(spelling); err == nil {
			t.Fatalf("deleting the skill root through %q must be refused", spelling)
		}
	}
	if _, err := os.Stat(rootDoc); err != nil {
		t.Fatalf("root-level SKILL.md vanished after refused delete: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling skill vanished after refused delete: %v", err)
	}
}
