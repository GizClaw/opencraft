package desktop

import (
	"testing"
	"time"
)

func TestPetStepMovesAndClamps(t *testing.T) {
	tick := 60 * time.Millisecond
	step := int(float64(petRoamSpeed) * tick.Seconds())
	if step <= 0 {
		t.Fatalf("roam step must be positive, got %d", step)
	}
	left := petStep(100, 200, tick)
	if left <= 100 || left > 200 {
		t.Fatalf("forward step = %d, want (100, 200]", left)
	}
	right := petStep(200, 100, tick)
	if right >= 200 || right < 100 {
		t.Fatalf("backward step = %d, want [100, 200)", right)
	}
	if got := petStep(197, 200, tick); got != 200 {
		t.Fatalf("forward step must clamp at target, got %d", got)
	}
	if got := petStep(103, 100, tick); got != 100 {
		t.Fatalf("backward step must clamp at target, got %d", got)
	}
	if got := petStep(100, 100, tick); got != 100 {
		t.Fatalf("static step must stay put, got %d", got)
	}
}

func TestPetRoamDelayStaysInWindow(t *testing.T) {
	for i := 0; i < 50; i++ {
		delay := petRoamDelay()
		if delay < 3*time.Second || delay > 10*time.Second {
			t.Fatalf("petRoamDelay = %v, want [3s, 10s]", delay)
		}
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
