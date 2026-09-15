package pet

// Rect is a rectangle in DIP. Inside a WindowGeometry it is relative to
// the pet window's top-left corner; used for screens it is global.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Right and Bottom return the exclusive edges of the rectangle.
func (r Rect) Right() int  { return r.X + r.Width }
func (r Rect) Bottom() int { return r.Y + r.Height }

// Empty reports a rectangle that cannot hold anything.
func (r Rect) Empty() bool { return r.Width <= 0 || r.Height <= 0 }

// WindowGeometry is where the renderer actually drew the character
// inside the pet window, measured in DIP relative to the window. The
// window is a 168px stage while the character is roughly 84px wide and
// sits below centre, so every placement has to be computed from these
// boxes: aligning the window rectangle instead parks the pet in the
// air, half a character away from whatever it is supposed to stand on.
type WindowGeometry struct {
	// Canvas is the Rive canvas inside the window.
	Canvas Rect `json:"canvas"`
	// Art is the drawn character's bounding box: the alpha bounds the
	// surface measured, or the canvas box when it could not measure.
	Art Rect `json:"art"`
	// Measured says whether Art came from real pixels instead of the
	// canvas fallback.
	Measured bool `json:"measured"`
}

// DefaultWindowGeometry is the layout that ships with the desktop: a
// 128px Rive canvas centred on the 168px stage (assistantPetWidth/Height
// and frontend/src/pet/pet.css) plus the built-in character's measured
// box inside it. It is what the window loop parks with until the
// renderer reports its own numbers.
func DefaultWindowGeometry() WindowGeometry {
	return WindowGeometry{
		Canvas: Rect{X: 20, Y: 20, Width: 128, Height: 128},
		Art:    Rect{X: 42, Y: 50, Width: 84, Height: 82},
	}
}

// NormalizeWindowGeometry returns a geometry the placement maths can
// rely on: a report that carries nothing usable falls back to the canvas
// box and then to the shipped default. It is also the gate for values
// that arrive from the renderer over the binding.
func NormalizeWindowGeometry(g WindowGeometry) WindowGeometry {
	if g.Art.Empty() {
		g.Art = g.Canvas
	}
	if g.Art.Empty() {
		return DefaultWindowGeometry()
	}
	return g
}

// Spacing around the character. The margins are deliberately expressed
// against the drawn box, not the window: "16px from the screen edge"
// means the character, which is what the eye checks.
const (
	// petDockMarginRight/petDockMarginBottom park the character in the
	// bottom-right corner of a work area.
	petDockMarginRight  = 16
	petDockMarginBottom = 8
	// petWatchInsetRight pulls the character inside the main window's
	// right edge, and petWatchFootOverlap lets its feet overlap the
	// window's bottom edge so it reads as standing on the frame.
	petWatchInsetRight  = 24
	petWatchFootOverlap = 2
	// petEdgeMargin keeps the character off any screen edge when a
	// target has to be clamped.
	petEdgeMargin = 8
)

// DockSpot returns the window position that parks the character in the
// bottom-right corner of a work area.
func DockSpot(g WindowGeometry, work Rect) (x, y int) {
	g = NormalizeWindowGeometry(g)
	return clampCharacter(
		g,
		work.Right()-petDockMarginRight-g.Art.Right(),
		work.Bottom()-petDockMarginBottom-g.Art.Bottom(),
		work,
	)
}

// WatchSpot returns the window position that stands the character on
// the main window's bottom-right edge: feet overlapping the bottom edge
// and the body pulled inside the right edge. A main window against a
// screen edge gets the closest reachable spot instead of an off-screen
// one.
func WatchSpot(g WindowGeometry, main, work Rect) (x, y int) {
	g = NormalizeWindowGeometry(g)
	return clampCharacter(
		g,
		main.Right()-petWatchInsetRight-g.Art.Right(),
		main.Bottom()+petWatchFootOverlap-g.Art.Bottom(),
		work,
	)
}

// clampCharacter shifts a window position until the character's drawn
// box sits inside the work area, keeping the window itself whole.
func clampCharacter(g WindowGeometry, x, y int, work Rect) (int, int) {
	minX := work.X + petEdgeMargin - g.Art.X
	maxX := work.Right() - petEdgeMargin - g.Art.Right()
	minY := work.Y + petEdgeMargin - g.Art.Y
	maxY := work.Bottom() - petEdgeMargin - g.Art.Bottom()
	return clampInt(x, minX, maxX), clampInt(y, minY, maxY)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
