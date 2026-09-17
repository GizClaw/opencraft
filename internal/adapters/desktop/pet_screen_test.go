package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
)

// The display the pet was parked on, and the smaller one left over once
// the machine came back from sleep with that monitor gone.
var (
	goneDisplay  = petfeed.Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	awakeDisplay = petfeed.Rect{X: 0, Y: 25, Width: 1512, Height: 982}
)

// A position the OS reports on a display is adopted as it is: the rover
// has to steer from the window that exists, not from the anchor it kept
// while the machine slept.
func TestPetAnchorAdoptsAnOnScreenPosition(t *testing.T) {
	g := petfeed.DefaultWindowGeometry()
	x, y := petfeed.DockSpot(g, awakeDisplay)
	gotX, gotY, moved := petAnchor(g, x, y, []petfeed.Rect{awakeDisplay})
	if gotX != x || gotY != y || moved {
		t.Fatalf("anchor = (%d,%d) moved=%v, want (%d,%d) moved=false",
			gotX, gotY, moved, x, y)
	}

	// The OS may have relocated the window while the machine slept.
	relocated := x - 700
	gotX, gotY, moved = petAnchor(
		g, relocated, y, []petfeed.Rect{awakeDisplay})
	if gotX != relocated || gotY != y || moved {
		t.Fatalf("relocated anchor = (%d,%d) moved=%v, want (%d,%d) moved=false",
			gotX, gotY, moved, relocated, y)
	}
}

// A pet parked against a display layout that is gone is pulled back onto
// the display that remains — the case a sleep/wake cycle leaves behind.
func TestPetAnchorPullsAStrandedPetBack(t *testing.T) {
	g := petfeed.DefaultWindowGeometry()
	x, y := petfeed.DockSpot(g, goneDisplay)
	works := []petfeed.Rect{awakeDisplay}
	if petfeed.CharacterOnScreen(g, x, y, works) {
		t.Fatalf("the fixture must start off-screen: (%d,%d)", x, y)
	}

	gotX, gotY, moved := petAnchor(g, x, y, works)
	if !moved {
		t.Fatalf("a stranded pet has to move: (%d,%d)", gotX, gotY)
	}
	if gotX == x && gotY == y {
		t.Fatal("the anchor must not stay off-screen")
	}
	if !petfeed.CharacterOnScreen(g, gotX, gotY, works) {
		t.Fatalf("anchor (%d,%d) is still off-screen", gotX, gotY)
	}
}

// With no display to anchor on there is nothing to do but keep the
// window where it is until one reports a usable work area.
func TestPetAnchorWithoutDisplays(t *testing.T) {
	g := petfeed.DefaultWindowGeometry()
	gotX, gotY, moved := petAnchor(g, 5000, 5000, nil)
	if gotX != 5000 || gotY != 5000 || moved {
		t.Fatalf("anchor = (%d,%d) moved=%v, want (5000,5000) moved=false",
			gotX, gotY, moved)
	}
}

// petWorkAreas drops what placement cannot anchor on: a nil screen entry
// and the empty rectangle a display reports while its configuration is
// settling.
func TestPetWorkAreasSkipsScreensThatCannotHoldThePet(t *testing.T) {
	screens := []*application.Screen{
		nil,
		{WorkArea: application.Rect{}},
		{WorkArea: application.Rect{X: 0, Y: 25, Width: 1512, Height: 982}},
		{WorkArea: application.Rect{X: 1512, Y: 0, Width: 1920, Height: 1080}},
	}
	want := []petfeed.Rect{
		{X: 0, Y: 25, Width: 1512, Height: 982},
		{X: 1512, Y: 0, Width: 1920, Height: 1080},
	}
	got := petWorkAreas(screens)
	if len(got) != len(want) {
		t.Fatalf("work areas = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("work area %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if works := petWorkAreas(nil); len(works) != 0 {
		t.Fatalf("no screens must yield no work areas, got %v", works)
	}
}
