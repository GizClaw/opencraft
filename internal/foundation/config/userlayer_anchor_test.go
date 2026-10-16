package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yamlv4 "go.yaml.in/yaml/v4"
)

// A merge can drop the section that carried a YAML anchor while a
// preserved key still refers to it - here the provider resource is
// replaced wholesale (dropProviderKeys) while another resource keeps
// its reference to the provider's settings. Writing that out unchanged
// produced a user layer with an unknown anchor: the app then failed to
// load a file it had written itself, on the next start.
func TestMergeUserLayerExpandsAliasesWhoseAnchorWasDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencraft.yaml")
	old := `resources:
  provider.openai: &shared
    model: gpt-5
  other:
    ref: *shared
    keep: 1
`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := `resources:
  other:
    add: 2
`
	out, err := mergeUserLayer(path, []byte(fresh),
		map[string]bool{}, map[string]bool{"other": true},
		map[string]bool{}, true)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if strings.Contains(string(out), "*shared") {
		t.Fatalf("merged layer still refers to a dropped anchor:\n%s", out)
	}
	assertAliasContent(t, out, "gpt-5", 1, 2)
}

// The merge reorders the document: generator-owned sections come first,
// preserved top-level sections are appended. An alias whose anchor was
// defined above it in the user's file therefore ends up ahead of the
// anchor - the name is still in the document and the file still does
// not load, because a YAML alias can only look backwards.
func TestMergeUserLayerExpandsAliasesThatEndUpBeforeTheirAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencraft.yaml")
	old := `defaults: &shared
  model: gpt-5
resources:
  other:
    ref: *shared
`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := `resources:
  other:
    add: 2
`
	out, err := mergeUserLayer(path, []byte(fresh),
		map[string]bool{}, map[string]bool{"other": true},
		map[string]bool{}, true)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if strings.Contains(string(out), "*shared") {
		t.Fatalf("merged layer kept a forward reference:\n%s", out)
	}
	assertAliasContent(t, out, "gpt-5", 0, 2)

	// The layer stays mergeable, which is the property that broke: a
	// file the app cannot read back is a file the app will lose.
	if _, err := mergeUserLayer(path, out, map[string]bool{},
		map[string]bool{"other": true}, map[string]bool{}, true); err != nil {
		t.Fatalf("merged layer cannot be merged again: %v", err)
	}
}

// An anchor and its alias that survive in reading order are left as
// they are: the rewrite repairs broken references, it does not flatten
// the user's document.
func TestMergeUserLayerKeepsAliasesThatStillLookBackwards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencraft.yaml")
	old := `resources:
  base: &shared
    model: gpt-5
  other:
    ref: *shared
`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nothing in the fresh document touches these two resources, so
	// they are appended in the order the user wrote them: the anchor
	// still comes first in the merged output.
	fresh := `resources:
  ours:
    x: 1
`
	out, err := mergeUserLayer(path, []byte(fresh),
		map[string]bool{}, map[string]bool{},
		map[string]bool{}, true)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(string(out), "&shared") ||
		!strings.Contains(string(out), "*shared") {
		t.Fatalf("a resolvable anchor was rewritten:\n%s", out)
	}
	assertAliasContent(t, out, "gpt-5", 0, 0)
}

// assertAliasContent parses a merged layer and checks that the resource
// the alias stood for still carries the anchored mapping, with the keys
// the merge was supposed to keep or add.
func assertAliasContent(t *testing.T, out []byte, model string, keep, add int) {
	t.Helper()
	var doc struct {
		Defaults  map[string]string `yaml:"defaults"`
		Resources map[string]struct {
			Ref  map[string]string `yaml:"ref"`
			Keep int               `yaml:"keep"`
			Add  int               `yaml:"add"`
		} `yaml:"resources"`
	}
	if err := yamlv4.Unmarshal(out, &doc); err != nil {
		t.Fatalf("merged layer does not parse: %v\n%s", err, out)
	}
	other := doc.Resources["other"]
	if other.Ref["model"] != model {
		t.Fatalf("alias content = %+v, want model %q", other.Ref, model)
	}
	if other.Keep != keep || other.Add != add {
		t.Fatalf("merged resource = %+v, want keep=%d add=%d",
			other, keep, add)
	}
}
