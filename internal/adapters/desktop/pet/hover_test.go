package pet

import "testing"

// The shipped layout puts the drawn character at (42, 50) 84x82 inside
// a 168px window, so a window at (100, 100) has its character between
// (142, 150) and (226, 232) on screen.
func TestCursorOnCharacterFollowsTheDrawnBox(t *testing.T) {
	geometry := DefaultWindowGeometry()
	cases := []struct {
		name string
		x, y int
		want bool
	}{
		{"centre of the character", 184, 190, true},
		{"top-left corner of the drawn box", 142, 150, true},
		{"just inside the pad", 138, 146, true},
		{"past the pad on the left", 137, 190, false},
		{"past the pad above", 184, 145, false},
		{"right edge of the drawn box", 226, 190, true},
		{"past the pad on the right", 231, 190, false},
		{"below the drawn box", 184, 240, false},
		// The window's own transparent margin must not count: the pointer
		// is 20px inside the window and 38px away from the character.
		{"transparent stage margin", 105, 105, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CursorOnCharacter(tc.x, tc.y, 100, 100, geometry)
			if got != tc.want {
				t.Fatalf("CursorOnCharacter(%d, %d) = %v, want %v",
					tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestCursorOnCharacterFallsBackToTheShippedLayout(t *testing.T) {
	// A renderer that has not reported yet leaves the geometry empty; the
	// default layout still answers sensibly.
	if !CursorOnCharacter(184, 190, 100, 100, WindowGeometry{}) {
		t.Fatal("an unmeasured pet must still react to the pointer on it")
	}
	if CursorOnCharacter(105, 105, 100, 100, WindowGeometry{}) {
		t.Fatal("an unmeasured pet must not react to its transparent margin")
	}
}
