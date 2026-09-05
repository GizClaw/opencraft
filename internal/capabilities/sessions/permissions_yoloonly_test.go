//go:build yoloonly

package sessions

import (
	"context"
	"testing"
)

func TestYoloOnlyStoreForcesMode(t *testing.T) {
	ctx := context.Background()
	s := &Store{}

	if mode, err := s.Mode(ctx, "s-1"); err != nil || mode != ModeYOLO {
		t.Fatalf("Mode = (%q, %v), want yolo", mode, err)
	}
	if err := s.SetMode(ctx, "s-1", ModeWorkspace); err == nil {
		t.Fatal("SetMode accepted workspace in the yoloonly build")
	}
	if err := s.SetMode(ctx, "s-1", ModeReadOnly); err == nil {
		t.Fatal("SetMode accepted read-only in the yoloonly build")
	}
	if err := s.SetMode(ctx, "s-1", ModeYOLO); err != nil {
		t.Fatalf("SetMode rejected yolo: %v", err)
	}
}
