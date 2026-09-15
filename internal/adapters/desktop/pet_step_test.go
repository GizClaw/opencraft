package desktop

import (
	"testing"
	"time"

	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
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

func TestPetFacing(t *testing.T) {
	if got := petFacing("", 6); got != petFacingRight {
		t.Fatalf("step right = %q, want %q", got, petFacingRight)
	}
	if got := petFacing(petFacingLeft, 6); got != petFacingRight {
		t.Fatalf("turn right = %q, want %q", got, petFacingRight)
	}
	if got := petFacing("", -6); got != petFacingLeft {
		t.Fatalf("step left = %q, want %q", got, petFacingLeft)
	}
	if got := petFacing(petFacingRight, -6); got != petFacingLeft {
		t.Fatalf("turn left = %q, want %q", got, petFacingLeft)
	}
	// No sideways movement means standing still, sleeping, a user drag
	// or an OS re-anchor: the last direction has to stay put.
	for _, prev := range []string{"", petFacingLeft, petFacingRight} {
		if got := petFacing(prev, 0); got != prev {
			t.Fatalf("hold %q = %q, want %q", prev, got, prev)
		}
	}
}

// The pet walks to its watch spot only while the agent works or asks:
// idling and sleeping both mean standing still, and a user drag always
// wins. This is the property the old random roaming violated.
func TestPetWalksToWatch(t *testing.T) {
	cases := []struct {
		name        string
		disposition petfeed.PetDisposition
		watchOK     bool
		manual      bool
		want        bool
	}{
		{"work walks", petfeed.PetDispositionWork, true, false, true},
		{"ask walks", petfeed.PetDispositionAsk, true, false, true},
		{"idle stays", petfeed.PetDispositionRoam, true, false, false},
		{"sleep stays", petfeed.PetDispositionSleep, true, false, false},
		{"no watch spot stays", petfeed.PetDispositionWork, false, false, false},
		{"drag wins", petfeed.PetDispositionWork, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := petWalksToWatch(tc.disposition, tc.watchOK, tc.manual)
			if got != tc.want {
				t.Fatalf("petWalksToWatch(%q, watchOK=%v, manual=%v) = %v, want %v",
					tc.disposition, tc.watchOK, tc.manual, got, tc.want)
			}
		})
	}
}

// A drag turns the character only after enough sideways travel: a slow
// drag accumulates, a vertical drag keeps the last direction, and the
// walk cycle follows any movement.
func TestPetDragStepTurnsOnAccumulatedTravel(t *testing.T) {
	d := &Desktop{}
	d.notePetDragStep(3, 0)
	if d.petDragFacing != "" {
		t.Fatalf("a wobble below the dead zone must not turn: %q", d.petDragFacing)
	}
	d.notePetDragStep(3, 0)
	if d.petDragFacing != petFacingRight {
		t.Fatalf("accumulated rightward drag = %q, want %q",
			d.petDragFacing, petFacingRight)
	}
	if d.petDragMovedAt.IsZero() {
		t.Fatal("a drag step must mark the gesture as moving")
	}

	d.notePetDragStep(0, 12)
	if d.petDragFacing != petFacingRight {
		t.Fatalf("a vertical drag must keep the facing, got %q", d.petDragFacing)
	}

	d.notePetDragStep(-20, 0)
	if d.petDragFacing != petFacingLeft {
		t.Fatalf("leftward drag = %q, want %q", d.petDragFacing, petFacingLeft)
	}
}
