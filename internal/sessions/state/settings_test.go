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

func TestSessionSettingsModel(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// Missing row returns "" so the caller applies the default policy.
	if model, err := s.Model(ctx, "s-missing"); err != nil || model != "" {
		t.Fatalf("Model(missing) = %q, %v; want \"\", nil", model, err)
	}

	if err := s.SetModel(ctx, "s-1", "deepseek/deepseek-v4-flash"); err != nil {
		t.Fatal(err)
	}
	if model, err := s.Model(ctx, "s-1"); err != nil || model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("Model(s-1) = %q, %v; want deepseek/deepseek-v4-flash, nil", model, err)
	}

	// Upsert replaces the previous value; an empty value resets to the
	// default policy.
	if err := s.SetModel(ctx, "s-1", ""); err != nil {
		t.Fatal(err)
	}
	if model, err := s.Model(ctx, "s-1"); err != nil || model != "" {
		t.Fatalf("Model(s-1) after reset = %q, %v; want \"\", nil", model, err)
	}
}

func TestSessionSettingsRemove(t *testing.T) {
	ctx := context.Background()
	s, err := Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if err := s.SetThinkLevel(ctx, "s-1", "high"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel(ctx, "s-1", "openai/gpt-5.6-sol"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSettings(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}
	if level, err := s.ThinkLevel(ctx, "s-1"); err != nil || level != "" {
		t.Fatalf("ThinkLevel after remove = %q, %v; want \"\", nil", level, err)
	}
	if model, err := s.Model(ctx, "s-1"); err != nil || model != "" {
		t.Fatalf("Model after remove = %q, %v; want \"\", nil", model, err)
	}
	// Removing an unknown session is a no-op.
	if err := s.RemoveSettings(ctx, "s-nope"); err != nil {
		t.Fatalf("RemoveSettings(unknown): %v", err)
	}
}
