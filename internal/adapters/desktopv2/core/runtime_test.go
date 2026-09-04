package core

import (
	"context"
	"testing"
)

func TestRuntimeOpenUserDB(t *testing.T) {
	dir := t.TempDir()
	rt := NewRuntime(dir, dir)
	t.Cleanup(rt.Close)

	if err := rt.OpenUserDB(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.Usage() == nil {
		t.Fatal("usage store not attached")
	}
	if rt.Automations() == nil {
		t.Fatal("automation store not attached")
	}
	if err := rt.OpenUserDB(context.Background()); err != nil {
		t.Fatalf("second OpenUserDB should be idempotent: %v", err)
	}
}
