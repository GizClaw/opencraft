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

// TestMatchStoredKeysStableID pins the identity path: only persisted
// stable ids inherit keys, so reordering AND editing a row still keeps
// its own key, while a brand-new row (no stable id) gets no key.
func TestMatchStoredKeysStableID(t *testing.T) {
	existing := []Instance{
		{StableID: "inst-a", Type: "deepseek", Name: "", Models: []Model{{Name: "m1"}}, API: "responses", KeySource: KeyLiteral, KeyValue: "k1"},
		{StableID: "inst-b", Type: "deepseek", Name: "", Models: []Model{{Name: "m2"}}, API: "responses", KeySource: KeyLiteral, KeyValue: "k2"},
	}

	// Reorder + edit while keeping stable ids: each row keeps its own key.
	idxs, ok := MatchStoredKeys(existing, []KeyRequest{
		{StableID: "inst-b", Type: "deepseek", Models: []string{"m9"}, API: "responses"},
		{StableID: "inst-a", Type: "deepseek", Models: []string{"m1"}, API: "responses"},
	}, map[int]bool{})
	if !ok {
		t.Fatal("stable-id rows must match")
	}
	if existing[idxs[0]].KeyValue != "k2" || existing[idxs[1]].KeyValue != "k1" {
		t.Fatalf("stable-id keys misattributed: row1 -> %q (idx %d), row2 -> %q (idx %d), want k2/k1",
			existing[idxs[0]].KeyValue, idxs[0], existing[idxs[1]].KeyValue, idxs[1])
	}

	// A brand-new row (no stable id) never inherits an existing key,
	// and a row whose stable id names a different type must not match.
	idxs, ok = MatchStoredKeys(existing, []KeyRequest{
		{StableID: "inst-b", Type: "deepseek", Models: []string{"m9"}, API: "responses"},
		{Type: "deepseek", Models: []string{"m3"}, API: "responses"},
	}, map[int]bool{})
	if idxs[0] != 1 {
		t.Fatalf("stable row idx = %d, want 1", idxs[0])
	}
	if idxs[1] != -1 {
		t.Fatalf("new row idx = %d, want -1", idxs[1])
	}
	if ok {
		t.Fatal("unmatched new row must report ok=false")
	}

	if _, ok := MatchStoredKeys(existing, []KeyRequest{
		{StableID: "inst-a", Type: "openai", Models: []string{"g"}},
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
