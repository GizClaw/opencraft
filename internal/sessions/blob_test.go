package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadState(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	type doc struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	if err := store.WriteState(id, "plans", map[string]doc{
		"assistant": {Name: "agent a", N: 1},
	}); err != nil {
		t.Fatal(err)
	}
	var got map[string]doc
	if err := store.ReadState(id, "plans", &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["assistant"].Name != "agent a" {
		t.Fatalf("read state = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(store.dir(id), "plans.json")); err != nil {
		t.Fatalf("plans.json: %v", err)
	}
}

func TestReadStateMissing(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := store.Create()
	if err := store.ReadState(id, "plans", &map[string]any{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing read = %v, want os.ErrNotExist", err)
	}
}
