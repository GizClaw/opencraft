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
	home := t.TempDir()
	t.Setenv("HOME", home)
	extra := filepath.Join(home, "extra-skills")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	rootDoc := filepath.Join(extra, "SKILL.md")
	if err := os.WriteFile(rootDoc,
		[]byte("---\nname: rooted\ndescription: root-level skill\n---\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	sibling := writeSkill(t, extra, "other",
		"name: other\ndescription: sibling skill\n")

	svc := NewService(context.Background(), Options{
		Enabled:    true,
		UserDir:    filepath.Join(home, "user"),
		ExtraRoots: []string{extra},
	})

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
