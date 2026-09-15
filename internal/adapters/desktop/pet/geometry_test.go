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
