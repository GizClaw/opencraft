package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKVSetGetListDelete(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"storage:kv"},
	}, "")
	kv := NewKVStore(root)
	got, err := kv.Get("hello", "counter")
	if err != nil || got.Value != "" {
		t.Fatalf("initial Get = (%+v, %v)", got, err)
	}
	if err := kv.Set("hello", "counter", "3"); err != nil {
		t.Fatal(err)
	}
	if err := kv.Set("hello", "label", "hello world"); err != nil {
		t.Fatal(err)
	}
	got, _ = kv.Get("hello", "counter")
	if got.Value != "3" {
		t.Fatalf("counter = %q", got.Value)
	}
	list, err := kv.List("hello")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if err := kv.Delete("hello", "counter"); err != nil {
		t.Fatal(err)
	}
	got, _ = kv.Get("hello", "counter")
	if got.Value != "" {
		t.Fatal("counter still present after delete")
	}
}

func TestKVPersistsAcrossReopen(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"storage:kv"},
	}, "")
	if err := NewKVStore(root).Set("hello", "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	got, err := NewKVStore(root).Get("hello", "theme")
	if err != nil || got.Value != "dark" {
		t.Fatalf("reopened kv = (%+v, %v)", got, err)
	}
}

func TestKVValidationAndRemoveAll(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "hello", map[string]any{
		"id": "hello", "name": "Hello", "version": "0.1.0",
		"entry": "dist/index.js", "permissions": []string{"storage:kv"},
	}, "")
	kv := NewKVStore(root)
	if err := kv.Set("hello", "../evil", "x"); err == nil {
		t.Fatal("invalid key should fail")
	}
	if err := kv.Set("hello", "big", string(make([]byte, MaxKVValueBytes+1))); err == nil {
		t.Fatal("oversized value should fail")
	}
	if err := kv.Set("not-installed", "k", "v"); err == nil {
		t.Fatal("kv for a non-installed plugin should fail")
	}
	if err := kv.Set("hello", "k", "v"); err != nil {
		t.Fatal(err)
	}
	kv.RemoveAll("hello")
	if _, err := os.Stat(filepath.Join(root, ".data", "hello")); !os.IsNotExist(err) {
		t.Fatalf("kv data should be removed, stat err = %v", err)
	}
}
