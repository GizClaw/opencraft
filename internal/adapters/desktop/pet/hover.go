package pet

// cursorPad is how far outside the drawn box the pointer still counts as
// hovering. It keeps a cursor resting on the character's edge from
// flickering between states, and lets the pet react just before the
// pointer is exactly on it.
const cursorPad = 4

// CursorOnCharacter reports whether a global pointer position is on the
// character. windowX/windowY anchor the window-relative geometry boxes
// on screen, and the drawn box (plus cursorPad) is what the pointer has
// to be inside: the window itself is a transparent stage and would
// answer "yes" for its whole rectangle.
func CursorOnCharacter(
	cursorX, cursorY, windowX, windowY int,
	geometry WindowGeometry,
) bool {
	geometry = NormalizeWindowGeometry(geometry)
	left := windowX + geometry.Art.X - cursorPad
	top := windowY + geometry.Art.Y - cursorPad
	right := windowX + geometry.Art.Right() + cursorPad
	bottom := windowY + geometry.Art.Bottom() + cursorPad
	return cursorX >= left && cursorX < right &&
		cursorY >= top && cursorY < bottom
}
