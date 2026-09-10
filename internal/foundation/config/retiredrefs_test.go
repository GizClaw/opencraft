package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDoc seeds one user layer document and returns its path.
func writeDoc(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := UserLayerFile(dir)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearRetiredEnv unsets the retired names for one test and restores the
// previous environment, so a developer shell that exports one cannot
// change the expected outcome.
func clearRetiredEnv(t *testing.T) {
	t.Helper()
	for _, ref := range retiredRefs {
		previous, set := os.LookupEnv(ref.Env)
		if err := os.Unsetenv(ref.Env); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if !set {
				if err := os.Unsetenv(ref.Env); err != nil {
					t.Errorf("unset %s: %v", ref.Env, err)
				}
				return
			}
			if err := os.Setenv(ref.Env, previous); err != nil {
				t.Errorf("restore %s: %v", ref.Env, err)
			}
		})
	}
}

const copiedHookDoc = `version: v1
resources:
  box:
    settings:
      remote: false
agents:
  assistant:
    prepare:
      - type: opencraft.media
        settings:
          work_dir: ${env:OPEN_CRAFT_WORKDIR}
`

// TestFindRetiredRefsReportsLiveReferences covers the upgrade case: a
// hand-authored block copied from a pre-resolver build names an assembly
// variable that no longer exists.
func TestFindRetiredRefsReportsLiveReferences(t *testing.T) {
	clearRetiredEnv(t)
	dir := t.TempDir()
	writeDoc(t, dir, copiedHookDoc)

	refs, err := FindRetiredRefs(dir)
	if err != nil {
		t.Fatalf("FindRetiredRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want exactly one", refs)
	}
	got := refs[0]
	if got.Env != "OPEN_CRAFT_WORKDIR" || got.Ref != "${ocraft:WORKDIR}" {
		t.Fatalf("unexpected reference: %+v", got)
	}
	if got.Line != 11 {
		t.Fatalf("line = %d, want 11", got.Line)
	}
}

// TestFindRetiredRefsIgnoresResolvableAndCommentedNames verifies the
// report only covers references that are actually broken: an exported
// variable still expands, and a comment never does.
func TestFindRetiredRefsIgnoresResolvableAndCommentedNames(t *testing.T) {
	clearRetiredEnv(t)
	dir := t.TempDir()
	writeDoc(t, dir, `version: v1
# work_dir: ${env:OPEN_CRAFT_WORKDIR}
resources:
  provider.deepseek:
    settings:
      profiles:
        - secrets:
            api_key: ${env:DEEPSEEK_API_KEY}
`)
	refs, err := FindRetiredRefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("unexpected refs: %+v", refs)
	}

	t.Setenv("OPEN_CRAFT_WORKDIR", "/custom/work")
	writeDoc(t, dir, copiedHookDoc)
	refs, err = FindRetiredRefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("exported name reported: %+v", refs)
	}
}

// TestLoadFailsWithAnchoredError verifies the load error names the layer
// file, the offending line, the retired variable and its replacement
// instead of surfacing an unset environment variable from deployment.
func TestLoadFailsWithAnchoredError(t *testing.T) {
	clearRetiredEnv(t)
	dir := t.TempDir()
	path := writeDoc(t, dir, copiedHookDoc)

	mgr, err := Open(Options{UserDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mgr.Load(context.Background())
	if err == nil {
		t.Fatal("expected load to fail on the retired reference")
	}
	for _, want := range []string{
		path, ":11", "${env:OPEN_CRAFT_WORKDIR}", "${ocraft:WORKDIR}",
		"Diagnostics",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// TestRepairRetiredRefsDropsStaleDeclaration verifies the repair removes
// the copied entry, prunes the containers it leaves empty so the
// built-in layer supplies them again, keeps unrelated content, and saves
// the pre-repair document.
func TestRepairRetiredRefsDropsStaleDeclaration(t *testing.T) {
	clearRetiredEnv(t)
	dir := t.TempDir()
	path := writeDoc(t, dir, copiedHookDoc)

	res, err := RepairRetiredRefs(dir)
	if err != nil {
		t.Fatalf("RepairRetiredRefs: %v", err)
	}
	if res.File != path {
		t.Fatalf("file = %q, want %q", res.File, path)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "agents.assistant.prepare[0]" {
		t.Fatalf("removed = %v", res.Removed)
	}
	backup, err := os.ReadFile(res.Backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != copiedHookDoc {
		t.Fatalf("backup does not hold the original document:\n%s", backup)
	}
	repaired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(repaired), "agents:") {
		t.Fatalf("stale block survived:\n%s", repaired)
	}
	if strings.Contains(string(repaired), "OPEN_CRAFT_") {
		t.Fatalf("retired name survived:\n%s", repaired)
	}
	if !strings.Contains(string(repaired), "remote: false") {
		t.Fatalf("unrelated settings were dropped:\n%s", repaired)
	}
	if refs, err := FindRetiredRefs(dir); err != nil || len(refs) != 0 {
		t.Fatalf("refs after repair = %+v, err = %v", refs, err)
	}

	// A repaired layer is not repaired again.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RepairRetiredRefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Removed) != 0 || again.Backup != "" {
		t.Fatalf("second repair reported %+v", again)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("second repair rewrote the layer")
	}
}

// TestRepairRetiredRefsKeepsSiblingKeys verifies a mapping keeps its
// other settings: only the entry holding the retired reference goes.
func TestRepairRetiredRefsKeepsSiblingKeys(t *testing.T) {
	clearRetiredEnv(t)
	dir := t.TempDir()
	path := writeDoc(t, dir, `version: v1
resources:
  box:
    settings:
      root: ${env:OPEN_CRAFT_WORKDIR}
      remote: false
      writable_paths: ["${ocraft:CACHE}"]
`)
	res, err := RepairRetiredRefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "resources.box.settings.root" {
		t.Fatalf("removed = %v", res.Removed)
	}
	repaired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"remote: false", "${ocraft:CACHE}"} {
		if !strings.Contains(string(repaired), want) {
			t.Fatalf("sibling %q missing:\n%s", want, repaired)
		}
	}
	if strings.Contains(string(repaired), "root:") {
		t.Fatalf("stale key survived:\n%s", repaired)
	}
}

// TestRepairRetiredRefsNoopWhenNothingIsBroken verifies unrelated
// references (provider keys, user variables) never trigger a write.
func TestRepairRetiredRefsNoopWhenNothingIsBroken(t *testing.T) {
	clearRetiredEnv(t)
	dir := t.TempDir()
	body := `version: v1
resources:
  provider.deepseek:
    settings:
      profiles:
        - secrets:
            api_key: ${env:DEEPSEEK_API_KEY}
`
	path := writeDoc(t, dir, body)
	res, err := RepairRetiredRefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || res.Backup != "" {
		t.Fatalf("unexpected repair: %+v", res)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("layer changed:\n%s", data)
	}
}

// TestRepairRetiredRefsMissingLayer verifies a first launch is a no-op.
func TestRepairRetiredRefsMissingLayer(t *testing.T) {
	res, err := RepairRetiredRefs(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("RepairRetiredRefs: %v", err)
	}
	if len(res.Removed) != 0 || res.Backup != "" {
		t.Fatalf("unexpected repair: %+v", res)
	}
}
