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

// Contains reports whether a point lies on the rectangle. The right and
// bottom edges are exclusive, so a point exactly on the far edge belongs
// to whatever comes next.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.Right() && y >= r.Y && y < r.Bottom()
}

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

// petVisibleShare is how much of the drawn character has to overlap a
// work area before the pet counts as visible there. Strict overlap would
// call two pixels of an ear poking past an edge "on screen", which is not
// what the user sees; a quarter of the character is.
const petVisibleShare = 0.25

// CharacterOnScreen reports whether the character parked at (x,y) is
// still visible on one of the work areas. Placement parks the drawn box
// inside a work area, so a window that no longer overlaps one has been
// left behind by a display layout it was not computed against — a
// monitor unplugged while the machine slept, a resolution switch, or the
// primary display changing place.
func CharacterOnScreen(g WindowGeometry, x, y int, works []Rect) bool {
	box := characterBox(g, x, y)
	visible := petVisibleShare * float64(box.Width*box.Height)
	for _, work := range works {
		if work.Empty() {
			continue
		}
		if float64(overlapArea(box, work)) >= visible {
			return true
		}
	}
	return false
}

// ClampToScreens returns the window position that brings the character
// parked at (x,y) back onto the closest work area, keeping the whole
// drawn box petEdgeMargin inside its edges. Work areas that cannot hold
// anything are skipped; with none left the position is returned
// unchanged, because there is nothing to anchor on.
func ClampToScreens(g WindowGeometry, x, y int, works []Rect) (int, int) {
	box := characterBox(g, x, y)
	closest := Rect{}
	best := 0
	found := false
	for _, work := range works {
		if work.Empty() {
			continue
		}
		if d := boxDistance(box, work); !found || d < best {
			closest, best, found = work, d, true
		}
	}
	if !found {
		return x, y
	}
	return clampCharacter(g, x, y, closest)
}

// characterBox is the drawn character's box in global DIP for a window
// parked at (x,y): every screen question is about the pixels the user
// sees, not about the transparent stage around them.
func characterBox(g WindowGeometry, x, y int) Rect {
	g = NormalizeWindowGeometry(g)
	return Rect{
		X:      x + g.Art.X,
		Y:      y + g.Art.Y,
		Width:  g.Art.Width,
		Height: g.Art.Height,
	}
}

// overlapArea returns the area two rectangles share; zero when they do
// not meet.
func overlapArea(a, b Rect) int {
	width := min(a.Right(), b.Right()) - max(a.X, b.X)
	height := min(a.Bottom(), b.Bottom()) - max(a.Y, b.Y)
	if width <= 0 || height <= 0 {
		return 0
	}
	return width * height
}

// boxDistance is the squared distance from the middle of a box to a
// rectangle: zero while the middle is inside it. It only ever picks which
// work area a stranded character belongs to, so the metric just has to
// rank nearer areas lower.
func boxDistance(box, work Rect) int {
	dx := axisDistance(box.X+box.Width/2, work.X, work.Right())
	dy := axisDistance(box.Y+box.Height/2, work.Y, work.Bottom())
	return dx*dx + dy*dy
}

// axisDistance is how far value sits outside [low, high].
func axisDistance(value, low, high int) int {
	switch {
	case value < low:
		return low - value
	case value > high:
		return value - high
	default:
		return 0
	}
}

// clampCharacter shifts a window position until the character's drawn
// box sits inside the work area, keeping the window itself whole.
func clampCharacter(g WindowGeometry, x, y int, work Rect) (int, int) {
	g = NormalizeWindowGeometry(g)
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
