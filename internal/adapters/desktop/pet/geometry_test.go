package pet

import "testing"

// A 1512x982 work area starting below a 25px menu bar, the shape the
// shipped default geometry was measured against.
var testWorkArea = Rect{X: 0, Y: 25, Width: 1512, Height: 982}

func TestDockSpotParksTheCharacterInTheCorner(t *testing.T) {
	g := DefaultWindowGeometry()
	x, y := DockSpot(g, testWorkArea)

	// The drawn box, not the window, keeps the margin: art right edge
	// 16px from the work-area edge and its feet 8px above the bottom.
	if got := x + g.Art.Right(); got != testWorkArea.Right()-petDockMarginRight {
		t.Fatalf("art right edge = %d, want %d",
			got, testWorkArea.Right()-petDockMarginRight)
	}
	if got := y + g.Art.Bottom(); got != testWorkArea.Bottom()-petDockMarginBottom {
		t.Fatalf("art bottom = %d, want %d",
			got, testWorkArea.Bottom()-petDockMarginBottom)
	}
}

func TestWatchSpotStandsOnTheWindowEdge(t *testing.T) {
	g := DefaultWindowGeometry()
	main := Rect{X: 100, Y: 100, Width: 1000, Height: 700}

	x, y := WatchSpot(g, main, testWorkArea)

	if got := x + g.Art.Right(); got != main.Right()-petWatchInsetRight {
		t.Fatalf("art right edge = %d, want %d",
			got, main.Right()-petWatchInsetRight)
	}
	if got := y + g.Art.Bottom(); got != main.Bottom()+petWatchFootOverlap {
		t.Fatalf("art feet = %d, want %d (standing on the frame)",
			got, main.Bottom()+petWatchFootOverlap)
	}
}

func TestWatchSpotClampsToTheWorkArea(t *testing.T) {
	g := DefaultWindowGeometry()
	// A main window whose right edge is past the screen (multi-monitor
	// or an oversized window) must not drag the pet off the work area.
	main := Rect{X: 1000, Y: 100, Width: 1000, Height: 700}
	x, _ := WatchSpot(g, main, testWorkArea)

	if got := x + g.Art.Right(); got != testWorkArea.Right()-petEdgeMargin {
		t.Fatalf("art right edge = %d, want the clamped %d",
			got, testWorkArea.Right()-petEdgeMargin)
	}
}

func TestWatchSpotKeepsTheFeetOnScreen(t *testing.T) {
	g := DefaultWindowGeometry()
	// A maximised main window shares its bottom edge with the work area.
	main := Rect{X: 0, Y: testWorkArea.Y, Width: 1512, Height: 982}
	_, y := WatchSpot(g, main, testWorkArea)

	if got := y + g.Art.Bottom(); got != testWorkArea.Bottom()-petEdgeMargin {
		t.Fatalf("art feet = %d, want the clamped %d",
			got, testWorkArea.Bottom()-petEdgeMargin)
	}
}

func TestGeometryFallsBackWhenUnmeasured(t *testing.T) {
	// A pack that reports a canvas but no drawn pixels anchors on the
	// canvas box; a report with nothing usable falls back to the
	// shipped layout.
	canvasOnly := WindowGeometry{Canvas: Rect{X: 20, Y: 20, Width: 128, Height: 128}}
	x, _ := DockSpot(canvasOnly, testWorkArea)
	if want := testWorkArea.Right() - petDockMarginRight - canvasOnly.Canvas.Right(); x != want {
		t.Fatalf("canvas-only dock x = %d, want %d", x, want)
	}

	empty := WindowGeometry{}
	x, y := DockSpot(empty, testWorkArea)
	wantX, wantY := DockSpot(DefaultWindowGeometry(), testWorkArea)
	if x != wantX || y != wantY {
		t.Fatalf("empty geometry docked at (%d,%d), want the default (%d,%d)",
			x, y, wantX, wantY)
	}
}

func TestRectContainsIsHalfOpen(t *testing.T) {
	work := Rect{X: 10, Y: 20, Width: 100, Height: 50}
	for _, point := range []struct {
		x, y int
		want bool
	}{
		{10, 20, true},
		{109, 69, true},
		{110, 20, false},
		{10, 70, false},
		{9, 20, false},
		{10, 19, false},
	} {
		if got := work.Contains(point.x, point.y); got != point.want {
			t.Fatalf("Contains(%d, %d) = %v, want %v",
				point.x, point.y, got, point.want)
		}
	}
}

func TestCharacterOnScreenFollowsTheDrawnBox(t *testing.T) {
	g := DefaultWindowGeometry()
	dockX, dockY := DockSpot(g, testWorkArea)
	if !CharacterOnScreen(g, dockX, dockY, []Rect{testWorkArea}) {
		t.Fatal("a docked pet is on screen")
	}

	// Half the character hanging past the right edge is still somewhere
	// the user can see (and is how a user may park it by hand).
	halfX := testWorkArea.Right() - g.Art.Width/2 - g.Art.X
	if !CharacterOnScreen(g, halfX, dockY, []Rect{testWorkArea}) {
		t.Fatal("a half-visible character is on screen")
	}

	// A sliver does not count: this is the shape a stale coordinate
	// leaves behind after the display layout changed under the window.
	sliverX := testWorkArea.Right() - petEdgeMargin - g.Art.X
	if CharacterOnScreen(g, sliverX, dockY, []Rect{testWorkArea}) {
		t.Fatalf("a %dpx sliver must not count as visible", petEdgeMargin)
	}
}

func TestCharacterOnScreenIgnoresEmptyWorkAreas(t *testing.T) {
	g := DefaultWindowGeometry()
	x, y := DockSpot(g, testWorkArea)
	works := []Rect{{}, {Width: 0, Height: 100}, testWorkArea}
	if !CharacterOnScreen(g, x, y, works) {
		t.Fatal("an empty work area must not hide a real one")
	}
	if CharacterOnScreen(g, x, y, []Rect{{}, {Height: -1}}) {
		t.Fatal("empty work areas cannot hold the character")
	}
}

func TestClampToScreensBringsAStrandedPetBack(t *testing.T) {
	g := DefaultWindowGeometry()
	// A position computed against a wider layout: the box is past the
	// right and bottom edges of the work area that is left.
	x := testWorkArea.Right() + 400
	y := testWorkArea.Bottom() + 120
	gotX, gotY := ClampToScreens(g, x, y, []Rect{testWorkArea})
	if gotX == x && gotY == y {
		t.Fatal("an off-screen position has to move")
	}
	if want := testWorkArea.Right() - petEdgeMargin; gotX+g.Art.Right() != want {
		t.Fatalf("art right edge = %d, want the clamped %d",
			gotX+g.Art.Right(), want)
	}
	if want := testWorkArea.Bottom() - petEdgeMargin; gotY+g.Art.Bottom() != want {
		t.Fatalf("art bottom = %d, want the clamped %d",
			gotY+g.Art.Bottom(), want)
	}
}

func TestClampToScreensPicksTheClosestWorkArea(t *testing.T) {
	g := DefaultWindowGeometry()
	left := Rect{X: 0, Y: 0, Width: 1000, Height: 800}
	right := Rect{X: 1000, Y: 0, Width: 1000, Height: 800}

	// A pet stranded past the right edge comes back to the right-hand
	// screen — the closest place to where it was, not the primary
	// display's corner.
	x, y := ClampToScreens(g, 2400, 300, []Rect{left, right})
	if want := right.Right() - petEdgeMargin; x+g.Art.Right() != want {
		t.Fatalf("art right edge = %d, want the clamped %d",
			x+g.Art.Right(), want)
	}
	if y != 300 {
		t.Fatalf("a position the display can hold must stay: y = %d", y)
	}

	// A pet stranded below both screens goes to the nearer one.
	x, y = ClampToScreens(g, 1500, 900, []Rect{left, right})
	if x != 1500 {
		t.Fatalf("x = %d, want the right-hand screen's 1500", x)
	}
	if want := right.Bottom() - petEdgeMargin; y+g.Art.Bottom() != want {
		t.Fatalf("art bottom = %d, want the clamped %d",
			y+g.Art.Bottom(), want)
	}
}

func TestClampToScreensKeepsAVisiblePosition(t *testing.T) {
	g := DefaultWindowGeometry()
	x, y := DockSpot(g, testWorkArea)
	gotX, gotY := ClampToScreens(g, x, y, []Rect{testWorkArea})
	if gotX != x || gotY != y {
		t.Fatalf("a visible position must not move: (%d,%d) -> (%d,%d)",
			x, y, gotX, gotY)
	}
	// No work area at all leaves the caller with nothing to anchor on.
	gotX, gotY = ClampToScreens(g, 5000, 5000, nil)
	if gotX != 5000 || gotY != 5000 {
		t.Fatalf("no work areas must keep the position, got (%d,%d)",
			gotX, gotY)
	}
}
