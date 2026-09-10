package desktop

import (
	"testing"
	"time"
)

func TestPetStepMovesAndClamps(t *testing.T) {
	tick := 60 * time.Millisecond
	const speed = 110 // the builtin pack's meta.walkSpeed
	if step := int(speed * tick.Seconds()); step <= 0 {
		t.Fatalf("roam step must be positive, got %d", step)
	}
	left := petStep(100, 200, tick, speed)
	if left <= 100 || left > 200 {
		t.Fatalf("forward step = %d, want (100, 200]", left)
	}
	right := petStep(200, 100, tick, speed)
	if right >= 200 || right < 100 {
		t.Fatalf("backward step = %d, want [100, 200)", right)
	}
	if got := petStep(197, 200, tick, speed); got != 200 {
		t.Fatalf("forward step must clamp at target, got %d", got)
	}
	if got := petStep(103, 100, tick, speed); got != 100 {
		t.Fatalf("backward step must clamp at target, got %d", got)
	}
	if got := petStep(100, 100, tick, speed); got != 100 {
		t.Fatalf("static step must stay put, got %d", got)
	}
	// A slow pack still moves: sub-DIP steps would otherwise stall the
	// rover at the same coordinate forever.
	if got := petStep(100, 200, tick, 5); got != 101 {
		t.Fatalf("slow pack step = %d, want 101", got)
	}
}

func TestClampInt(t *testing.T) {
	if got := clampInt(5, 0, 10); got != 5 {
		t.Fatalf("mid value = %d", got)
	}
	if got := clampInt(-3, 0, 10); got != 0 {
		t.Fatalf("low value = %d", got)
	}
	if got := clampInt(15, 0, 10); got != 10 {
		t.Fatalf("high value = %d", got)
	}
}
