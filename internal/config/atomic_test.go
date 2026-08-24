package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "opencraft.yaml")
	if err := writeFileAtomic(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a: 1\n" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	// No temp litter remains in the directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "opencraft.yaml" {
			t.Fatalf("unexpected leftover %s", e.Name())
		}
	}
}

func TestMatchStoredKey(t *testing.T) {
	withKey := func(typ, name, model, endpoint, api, key string) Instance {
		return Instance{
			Type: typ, Name: name, Model: model,
			Endpoint: endpoint, API: api,
			KeySource: KeyLiteral, KeyValue: key,
		}
	}
	existing := []Instance{
		withKey("deepseek", "", "m1", "", "responses", "k1"),
		withKey("deepseek", "", "m2", "", "responses", "k2"),
		{Type: "openai", Name: "", Model: "g", KeySource: KeyEnv},
	}

	// Reordering: fingerprint match wins over ordinal, so B keeps k2.
	idx, ok := MatchStoredKey(existing, "deepseek", "", "m2", "", "responses", 1, map[int]bool{})
	if !ok || existing[idx].KeyValue != "k2" {
		t.Fatalf("fingerprint match = (%d, %v), want k2", idx, ok)
	}

	// Editing in place: no fingerprint match, ordinal fallback keeps k1.
	idx, ok = MatchStoredKey(existing, "deepseek", "", "m9", "", "responses", 1, map[int]bool{})
	if !ok || existing[idx].KeyValue != "k1" {
		t.Fatalf("ordinal match = (%d, %v), want k1", idx, ok)
	}

	// A brand-new instance has no stored key.
	if _, ok := MatchStoredKey(existing, "qwen", "", "q1", "", "", 1, map[int]bool{}); ok {
		t.Fatal("new instance must not match")
	}

	// Env-sourced keys are never inherited.
	if _, ok := MatchStoredKey(existing, "openai", "", "g", "", "", 1, map[int]bool{}); ok {
		t.Fatal("env-sourced key must not be inherited")
	}

	// Claimed tracking stops two request rows stealing the same key.
	claimed := map[int]bool{}
	first, ok := MatchStoredKey(existing, "deepseek", "", "m1", "", "responses", 1, claimed)
	if !ok || existing[first].KeyValue != "k1" {
		t.Fatalf("first claim = (%d, %v)", first, ok)
	}
	claimed[first] = true
	if _, ok := MatchStoredKey(existing, "deepseek", "", "m1", "", "responses", 1, claimed); ok {
		t.Fatal("claimed key must not be inherited twice")
	}
}
