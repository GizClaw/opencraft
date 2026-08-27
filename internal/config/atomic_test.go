package config

import (
	"os"
	"path/filepath"
	"strings"
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
	rows := func(pairs ...KeyRequest) []KeyRequest { return pairs }

	// Reordering: fingerprint matches win, so B keeps k2 and A keeps k1.
	idx, ok := MatchStoredKeys(existing, rows(
		KeyRequest{Type: "deepseek", Model: "m2", API: "responses"},
		KeyRequest{Type: "deepseek", Model: "m1", API: "responses"},
	), map[int]bool{})
	if !ok || existing[idx[0]].KeyValue != "k2" || existing[idx[1]].KeyValue != "k1" {
		t.Fatalf("reorder match = %v (%v), want k2,k1", idx, ok)
	}

	// Editing in place: edited row falls back to the leftover key.
	idx, ok = MatchStoredKeys(existing, rows(
		KeyRequest{Type: "deepseek", Model: "m9", API: "responses"},
		KeyRequest{Type: "deepseek", Model: "m2", API: "responses"},
	), map[int]bool{})
	if !ok || existing[idx[0]].KeyValue != "k1" || existing[idx[1]].KeyValue != "k2" {
		t.Fatalf("edit match = %v (%v), want k1,k2", idx, ok)
	}

	// A brand-new instance has no stored key.
	if _, ok := MatchStoredKeys(existing, rows(
		KeyRequest{Type: "qwen", Model: "q1"},
	), map[int]bool{}); ok {
		t.Fatal("new instance must not match")
	}

	// Env-sourced keys are never inherited.
	if _, ok := MatchStoredKeys(existing, rows(
		KeyRequest{Type: "openai", Model: "g"},
	), map[int]bool{}); ok {
		t.Fatal("env-sourced key must not be inherited")
	}

	// Claimed tracking stops a duplicate row stealing the same key.
	claimed := map[int]bool{}
	idx, ok = MatchStoredKeys(existing, rows(
		KeyRequest{Type: "deepseek", Model: "m1", API: "responses"},
		KeyRequest{Type: "deepseek", Model: "m1", API: "responses"},
	), claimed)
	if !ok || existing[idx[0]].KeyValue != "k1" || existing[idx[1]].KeyValue != "k2" {
		t.Fatalf("dup match = %v (%v), want k1,k2", idx, ok)
	}

	// More blank rows than stored keys still fail cleanly.
	if _, ok := MatchStoredKeys(existing, rows(
		KeyRequest{Type: "deepseek", Model: "m9", API: "responses"},
		KeyRequest{Type: "deepseek", Model: "m8", API: "responses"},
		KeyRequest{Type: "deepseek", Model: "m7", API: "responses"},
	), map[int]bool{}); ok {
		t.Fatal("rows without an available key must fail")
	}
}

// TestMatchStoredKeyReorderEdit reproduces the misattribution: with two
// same-type instances, editing one row's model AND reordering it while
// leaving the key blank makes the fingerprint and ordinal fallbacks
// contradict each other, silently swapping the two stored keys.
func TestMatchStoredKeyReorderEdit(t *testing.T) {
	existing := []Instance{
		{Type: "deepseek", Name: "", Model: "m1", API: "responses", KeySource: KeyLiteral, KeyValue: "k1"},
		{Type: "deepseek", Name: "", Model: "m2", API: "responses", KeySource: KeyLiteral, KeyValue: "k2"},
	}
	claimed := map[int]bool{}

	// Request row 1: old idx1 edited (m2 -> m9) and moved first, key blank.
	idxs, ok := MatchStoredKeys(existing, []KeyRequest{
		{Type: "deepseek", Model: "m9", API: "responses"},
		{Type: "deepseek", Model: "m1", API: "responses"},
	}, claimed)
	if !ok {
		t.Fatal("row1 should inherit a key")
	}
	idx1, idx2 := idxs[0], idxs[1]

	// Expect row1 -> k2 (idx1), row2 -> k1 (idx0).
	if existing[idx1].KeyValue != "k2" || existing[idx2].KeyValue != "k1" {
		t.Fatalf("keys swapped: row1 -> %q (idx %d), row2 -> %q (idx %d), want k2/k1",
			existing[idx1].KeyValue, idx1, existing[idx2].KeyValue, idx2)
	}
}

// TestMatchStoredKeysStableID pins the identity path: a persisted
// stable id wins over fingerprints, so reordering AND editing a row
// still keeps its own key, while a brand-new row (no stable id) can
// only take a leftover key.
func TestMatchStoredKeysStableID(t *testing.T) {
	existing := []Instance{
		{StableID: "inst-a", Type: "deepseek", Name: "", Model: "m1", API: "responses", KeySource: KeyLiteral, KeyValue: "k1"},
		{StableID: "inst-b", Type: "deepseek", Name: "", Model: "m2", API: "responses", KeySource: KeyLiteral, KeyValue: "k2"},
	}

	// Reorder + edit while keeping stable ids: each row keeps its own
	// key even though neither fingerprint matches anymore.
	idxs, ok := MatchStoredKeys(existing, []KeyRequest{
		{StableID: "inst-b", Type: "deepseek", Model: "m9", API: "responses"},
		{StableID: "inst-a", Type: "deepseek", Model: "m1", API: "responses"},
	}, map[int]bool{})
	if !ok {
		t.Fatal("stable-id rows must match")
	}
	if existing[idxs[0]].KeyValue != "k2" || existing[idxs[1]].KeyValue != "k1" {
		t.Fatalf("stable-id keys misattributed: row1 -> %q (idx %d), row2 -> %q (idx %d), want k2/k1",
			existing[idxs[0]].KeyValue, idxs[0], existing[idxs[1]].KeyValue, idxs[1])
	}

	// A brand-new row (no stable id) never steals a stable-id claim; it
	// takes the leftover same-type key, and a row whose stable id names
	// a different type must not inherit across providers.
	idxs, ok = MatchStoredKeys(existing, []KeyRequest{
		{StableID: "inst-b", Type: "deepseek", Model: "m9", API: "responses"},
		{Type: "deepseek", Model: "m3", API: "responses"},
	}, map[int]bool{})
	if !ok {
		t.Fatal("new row should fall back to the leftover key")
	}
	if existing[idxs[1]].KeyValue != "k1" {
		t.Fatalf("new row took %q (idx %d), want the leftover k1", existing[idxs[1]].KeyValue, idxs[1])
	}

	if _, ok := MatchStoredKeys(existing, []KeyRequest{
		{StableID: "inst-a", Type: "openai", Model: "g"},
	}, map[int]bool{}); ok {
		t.Fatal("stable id must not match across provider types")
	}
}

func TestNewStableID(t *testing.T) {
	a, b := NewStableID(), NewStableID()
	if a == "" || b == "" {
		t.Fatal("stable ids must not be empty")
	}
	if a == b {
		t.Fatalf("stable ids must differ: %q", a)
	}
	if !strings.HasPrefix(a, "inst-") {
		t.Fatalf("stable id %q has no inst- prefix", a)
	}
}
