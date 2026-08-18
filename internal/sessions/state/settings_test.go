package state

import (
	"context"
	"testing"
)

func TestSessionSettingsThinkLevel(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// Missing row returns "" so the caller applies its default.
	if level, err := s.ThinkLevel(ctx, "s-missing"); err != nil || level != "" {
		t.Fatalf("ThinkLevel(missing) = %q, %v; want \"\", nil", level, err)
	}

	if err := s.SetThinkLevel(ctx, "s-1", "high"); err != nil {
		t.Fatal(err)
	}
	if level, err := s.ThinkLevel(ctx, "s-1"); err != nil || level != "high" {
		t.Fatalf("ThinkLevel(s-1) = %q, %v; want high, nil", level, err)
	}

	// Upsert replaces the previous value.
	if err := s.SetThinkLevel(ctx, "s-1", "low"); err != nil {
		t.Fatal(err)
	}
	if level, err := s.ThinkLevel(ctx, "s-1"); err != nil || level != "low" {
		t.Fatalf("ThinkLevel(s-1) after upsert = %q, %v; want low, nil", level, err)
	}
}
