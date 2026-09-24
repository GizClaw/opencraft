package desktop

import (
	"context"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	otellog "go.opentelemetry.io/otel/log"

	desktopcore "github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The transparent stage the assistant pet lives on. The
// renderer centres a 128px Rive canvas on it, draws the speech bubble
// in the empty band above the character and the tool pill below its
// feet, so the stage only has to be as large as those three together.
//
// Keep it tight: a transparent webview still captures the mouse over
// its whole rectangle (Wails only offers whole-window mouse ignoring),
// so every pixel of margin here is a pixel that swallows a click meant
// for the window underneath. Presses that miss the character are
// dropped by the surface itself (frontend/src/pet/hit.ts), which is
// why the stage no longer has to be generous.
const (
	assistantPetWidth  = 168
	assistantPetHeight = 168
)

// petLoopTick is the window loop's physics step. The state broadcast
// runs on a slower cadence (see petStateBroadcastEvery).
const petLoopTick = 60 * time.Millisecond

// petManualHoldFor keeps autonomy paused after a user drag.
const petManualHoldFor = 5 * time.Second

// petDragTurnPixels is how far a drag has to travel sideways before the
// character turns. Below it the last direction is held, so a wobbling
// pointer does not flip the pose while the window is being dragged.
const petDragTurnPixels = 6

// petDragWalkIdle is how long the walk cycle survives after the last
// drag step. Dragging is a series of small moves with pauses between
// them; a pet that freezes mid-gesture looks carried rather than walking.
const petDragWalkIdle = 250 * time.Millisecond

// petStatePayload is the wire snapshot broadcast to the pet surface.
// It mirrors petfeed.PetSurfaceState so the renderer never imports Go
// domain packages through the Wails binding layer.
type petStatePayload struct {
	AgentID      string `json:"agent_id"`
	Phase        string `json:"phase"`
	ToolName     string `json:"tool_name,omitempty"`
	ToolCategory string `json:"tool_category,omitempty"`
	Disposition  string `json:"disposition"`
	Interactive  bool   `json:"interactive"`
	Walking      bool   `json:"walking,omitempty"`
	// Facing is the horizontal walk direction ("left"/"right") the rover
	// last stepped in. It stays empty until the pet walks and is held
	// while the pet stands still, so a character never snaps back to a
	// default pose between strolls.
	Facing   string `json:"facing,omitempty"`
	Sleeping bool   `json:"sleeping,omitempty"`
	Intent   string `json:"intent,omitempty"`
	// IntentSeq grows only when the intent changes, so the renderer
	// fires a one-shot trigger once instead of on every repeated
	// broadcast of the reaction it is already playing.
	IntentSeq uint64 `json:"intent_seq"`
	Bubble    string `json:"bubble,omitempty"`
}

func newPetStatePayload(st petfeed.PetSurfaceState) petStatePayload {
	p := petStatePayload{
		AgentID:     st.AgentID,
		Phase:       string(st.Phase),
		Disposition: string(st.Disposition),
		Interactive: st.Interactive,
		Sleeping:    st.Sleeping,
		Intent:      string(st.Intent),
		Bubble:      st.Bubble,
	}
	if st.Tool != nil {
		p.ToolName = st.Tool.Name
		p.ToolCategory = string(st.Tool.Category)
	}
	return p
}

// startAssistantPet creates the assistant pet window when the desktop
// pet preference is enabled and starts the window loop.
func (d *Desktop) startAssistantPet(ctx context.Context) {
	if !d.core.Shell.PetsEnabled() {
		return
	}
	d.petMu.Lock()
	running := d.petWindow != nil
	d.petMu.Unlock()
	if running {
		return
	}
	app := d.core.Shell.App()
	if app == nil {
		return
	}
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "pet",
		Title:            "OpenCraft Pet",
		URL:              "/?surface=pet&agent=assistant",
		Width:            assistantPetWidth,
		Height:           assistantPetHeight,
		MinWidth:         assistantPetWidth,
		MinHeight:        assistantPetHeight,
		MaxWidth:         assistantPetWidth,
		MaxHeight:        assistantPetHeight,
		DisableResize:    true,
		Frameless:        true,
		AlwaysOnTop:      true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Mac: application.MacWindow{
			Backdrop:      application.MacBackdropTransparent,
			DisableShadow: true,
			WindowLevel:   application.MacWindowLevelFloating,
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
		},
		Linux: application.LinuxWindow{
			WindowIsTranslucent: true,
			WebviewGpuPolicy:    application.WebviewGpuPolicyAlways,
		},
	})
	if win == nil {
		telemetry.Warn(context.Background(),
			"desktop: pet window creation failed")
		return
	}

	// The window stays interactive (not mouse-ignored) so the pet can
	// be dragged and clicked. Wails only offers whole-window mouse
	// ignoring, so real click-through stays a platform follow-up; the
	// surface drops presses that miss the drawn character instead
	// (frontend/src/pet/hit.ts).
	win.SetIgnoreMouseEvents(false)

	stop := make(chan struct{})
	director := petfeed.NewPetDirector(d.core.Pet)
	d.petMu.Lock()
	d.petWindow = win
	d.petStop = stop
	d.petDirector = director
	d.petGeometry = petfeed.DefaultWindowGeometry()
	d.petMu.Unlock()
	d.core.Shell.SetPetWindowControls(desktopcore.PetWindowControls{
		MoveBy:      d.movePetWindow,
		SetPosition: d.setPetWindowPosition,
		Activate:    d.activatePet,
		Poke:        d.pokePet,
		Diagnostics: d.petDiagnostics,
		Position:    d.petWindowPosition,
		Geometry:    d.setPetGeometry,
		BeginDrag:   d.beginPetDrag,
		EndDrag:     d.endPetDrag,
	})

	go d.runPetWindowLoop(ctx, stop, win, app, director)
}

// ensureAssistantPet starts the pet window when enabled and not already
// running.
func (d *Desktop) ensureAssistantPet(ctx context.Context) {
	if !d.core.Shell.PetsEnabled() {
		return
	}
	d.petMu.Lock()
	running := d.petWindow != nil
	d.petMu.Unlock()
	if running {
		return
	}
	d.startAssistantPet(ctx)
}

// onPetsChanged reacts to preference changes from the settings UI:
// enabling creates the window, disabling tears it down.
func (d *Desktop) onPetsChanged() {
	d.petMu.Lock()
	ctx := d.petCtx
	d.petMu.Unlock()
	if ctx == nil {
		return
	}
	if d.core.Shell.PetsEnabled() {
		d.ensureAssistantPet(ctx)
		return
	}
	d.stopPet()
}

// setPetGeometry records where the renderer drew the character inside
// the window. Every placement calculation (dock, watch spot, clamps)
// anchors on that box rather than on the transparent window rectangle.
func (d *Desktop) setPetGeometry(g petfeed.WindowGeometry) {
	d.petMu.Lock()
	d.petGeometry = petfeed.NormalizeWindowGeometry(g)
	d.petMu.Unlock()
}

// movePetWindow drags the pet window by a relative offset and pauses
// the autonomous rover for petManualHoldFor.
func (d *Desktop) movePetWindow(dx, dy int) {
	d.petMu.Lock()
	defer d.petMu.Unlock()
	if !d.petReady {
		return
	}
	win := d.petWindow
	if win == nil {
		return
	}
	d.petX += dx
	d.petY += dy
	d.petManualUntil = time.Now().Add(petManualHoldFor)
	win.SetPosition(d.petX, d.petY)
}

// setPetWindowPosition moves the pet window to an absolute position and
// pauses the autonomous rover for petManualHoldFor.
func (d *Desktop) setPetWindowPosition(x, y int) {
	d.petMu.Lock()
	defer d.petMu.Unlock()
	if !d.petReady {
		return
	}
	win := d.petWindow
	if win == nil {
		return
	}
	d.notePetDragStep(x-d.petX, y-d.petY)
	d.petX = x
	d.petY = y
	d.petManualUntil = time.Now().Add(petManualHoldFor)
	win.SetPosition(x, y)
}

// notePetDragStep turns one drag delta into the walk signal the Rive
// state machine reads: any movement keeps the walk cycle alive for
// petDragWalkIdle, and a sideways step past the dead zone turns the
// character. Callers hold petMu.
func (d *Desktop) notePetDragStep(dx, dy int) {
	if dx == 0 && dy == 0 {
		return
	}
	d.petDragMovedAt = time.Now()
	d.petDragDx += dx
	if absInt(d.petDragDx) < petDragTurnPixels {
		return
	}
	d.petDragFacing = petFacing(d.petDragFacing, d.petDragDx)
	d.petDragDx = 0
}

// beginPetDrag marks the start of a drag gesture. The character walks
// while the window is being moved instead of standing still.
func (d *Desktop) beginPetDrag() {
	d.petMu.Lock()
	d.petDragging = true
	d.petDragDx = 0
	d.petDragMovedAt = time.Time{}
	d.petManualUntil = time.Now().Add(petManualHoldFor)
	d.petMu.Unlock()
}

// endPetDrag ends the gesture; the pet stands still again.
func (d *Desktop) endPetDrag() {
	d.petMu.Lock()
	d.petDragging = false
	d.petDragDx = 0
	d.petDragMovedAt = time.Time{}
	d.petMu.Unlock()
}

// activatePet brings the main OpenCraft window to the foreground.
func (d *Desktop) activatePet() {
	d.core.Shell.FocusMain()
}

// pokePet records a click/pet interaction; the mind reacts on the next
// rover tick.
func (d *Desktop) pokePet() {
	d.petMu.Lock()
	director := d.petDirector
	d.petMu.Unlock()
	if director != nil {
		director.NotePoke(time.Now())
	}
}

// dockAssistantPet parks the pet window at the bottom-right corner of
// the primary screen's work area and records the position on Desktop.
// The drawn character, not the window, keeps the corner margins.
func (d *Desktop) dockAssistantPet(
	app *application.App, win *application.WebviewWindow,
) bool {
	screen := app.Screen.GetPrimary()
	if screen == nil {
		return false
	}
	area := screen.WorkArea
	work := petfeed.Rect{
		X: area.X, Y: area.Y, Width: area.Width, Height: area.Height,
	}
	d.petMu.Lock()
	geometry := d.petGeometry
	d.petMu.Unlock()
	x, y := petfeed.DockSpot(geometry, work)
	win.SetPosition(x, y)
	d.petMu.Lock()
	d.petX = x
	d.petY = y
	d.petReady = true
	d.petMu.Unlock()
	return true
}

// waitForPetDock retries dockAssistantPet until the Wails screen
// manager has the primary display (it can be empty right after window
// creation during startup). Returns false if the pet is stopped first.
func (d *Desktop) waitForPetDock(
	ctx context.Context,
	stop chan struct{},
	app *application.App,
	win *application.WebviewWindow,
) bool {
	deadline := time.Now().Add(6 * time.Second)
	for {
		if d.dockAssistantPet(app, win) {
			return true
		}
		select {
		case <-stop:
			return false
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// petWorkAreas returns the work areas the pet can stand on. A screen can
// report an empty rectangle while a display configuration settles (a
// wake, a hot-plug), and clamping into one would park the character
// nowhere, so those are dropped instead.
func petWorkAreas(screens []*application.Screen) []petfeed.Rect {
	var works []petfeed.Rect
	for _, screen := range screens {
		if screen == nil {
			continue
		}
		area := screen.WorkArea
		work := petfeed.Rect{
			X: area.X, Y: area.Y, Width: area.Width, Height: area.Height,
		}
		if work.Empty() {
			continue
		}
		works = append(works, work)
	}
	return works
}

// keepPetOnScreen keeps the pet window on a display the user can see. A
// desktop session outlives monitor unplugs, resolution switches and
// sleep/wake cycles, and every one of them rewrites what a window
// coordinate means: a window parked against the old layout keeps its
// numbers and ends up on no display at all, where nothing else in the
// rover brings it back (the watch spot is only reachable while the agent
// works, and the rover steers from its own anchor).
//
// The OS-reported position is the truth while it is on a display, so the
// rover adopts it; that is also what keeps the walk honest when the OS
// relocated the window itself instead of the rover. When the reported
// position is on none of the displays, the character is clamped back
// onto the closest work area: the nearest edge, not a jump to the
// primary display's corner.
func (d *Desktop) keepPetOnScreen(
	win *application.WebviewWindow, works []petfeed.Rect,
) {
	// A window the user cannot see — app hidden, minimised to the Dock or
	// taskbar — reports a placeholder position on some platforms, and
	// there is nothing on screen to pull back while it stays tucked away.
	if len(works) == 0 || !win.IsVisible() || win.IsMinimised() {
		return
	}
	now := time.Now()
	d.petMu.Lock()
	geometry := d.petGeometry
	held := d.petDragging || now.Before(d.petManualUntil)
	d.petMu.Unlock()
	if held {
		// The pointer owns the window for the duration of a drag and the
		// hold that follows it; the next cadence picks up whatever the
		// gesture left behind.
		return
	}
	osX, osY := win.Position()
	x, y, moved := petAnchor(geometry, osX, osY, works)
	if moved {
		win.SetPosition(x, y)
		telemetry.Info(context.Background(),
			"desktop: pet pulled back onto a display",
			otellog.Int64("from_x", int64(osX)),
			otellog.Int64("from_y", int64(osY)),
			otellog.Int64("to_x", int64(x)),
			otellog.Int64("to_y", int64(y)))
	}
	d.petMu.Lock()
	d.petX, d.petY = x, y
	d.petMu.Unlock()
}

// petAnchor reconciles the rover's anchor with the window the OS actually
// has. A position that is still on a display is adopted as it is — the OS
// is the authority on where the window is, and following it is also what
// keeps the walk honest when the OS itself relocated the window. A
// position on no display is clamped onto the closest work area, and moved
// reports that the window has to be put there.
func petAnchor(
	geometry petfeed.WindowGeometry, osX, osY int, works []petfeed.Rect,
) (int, int, bool) {
	if petfeed.CharacterOnScreen(geometry, osX, osY, works) {
		return osX, osY, false
	}
	x, y := petfeed.ClampToScreens(geometry, osX, osY, works)
	return x, y, x != osX || y != osY
}

// runPetWindowLoop keeps the pet window where it belongs: parked where
// the user left it, walking to the watch spot while the agent works or
// asks, and standing still otherwise. The pet never wanders on its own.
func (d *Desktop) runPetWindowLoop(
	ctx context.Context,
	stop chan struct{},
	win *application.WebviewWindow,
	app *application.App,
	director *petfeed.PetDirector,
) {
	if !d.waitForPetDock(ctx, stop, app, win) {
		return
	}
	d.petMu.Lock()
	x, y := d.petX, d.petY
	d.petMu.Unlock()

	if app.Screen.GetPrimary() == nil {
		return
	}
	// mainRect is the main window's frame and perchArea the work area of
	// the screen it sits on; both are re-read on a slow cadence below, on
	// the same display snapshot the pet is kept on.
	mainRect := petfeed.Rect{X: x, Y: y}
	perchArea := petfeed.Rect{}
	perchOK := false
	var works []petfeed.Rect
	lastRectRefresh := time.Now().Add(-time.Second)
	ticker := time.NewTicker(petLoopTick)
	defer ticker.Stop()

	var last petStatePayload
	moved := false
	// facing carries the last walk direction across ticks; petFacing
	// keeps it when the pet does not move sideways.
	facing := ""
	var state petfeed.PetSurfaceState
	var intents petfeed.IntentSequencer
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()

			if now.Sub(lastRectRefresh) >= 500*time.Millisecond {
				lastRectRefresh = now
				perchOK = false
				// A desktop session outlives monitor unplugs, resolution
				// switches and sleep/wake cycles, and each of them
				// rewrites what a window coordinate means: the pet is
				// re-anchored on the display layout of the moment before
				// anything else steers it.
				works = petWorkAreas(app.Screen.GetAll())
				d.keepPetOnScreen(win, works)
				// A minimised or tray-hidden window is not something to
				// walk to: its reported position is stale, and the pet
				// would end up standing next to nothing.
				if main := d.core.Shell.MainWindow(); main != nil &&
					main.IsVisible() && !main.IsMinimised() {
					mainX, mainY := main.Position()
					mainW, mainH := main.Size()
					mainRect = petfeed.Rect{
						X: mainX, Y: mainY, Width: mainW, Height: mainH,
					}
					centerX := mainRect.X + mainRect.Width/2
					centerY := mainRect.Y + mainRect.Height/2
					for _, work := range works {
						if work.Contains(centerX, centerY) {
							perchArea = work
							perchOK = true
							break
						}
					}
				}
			}
			d.petMu.Lock()
			manual := now.Before(d.petManualUntil)
			// A drag gesture counts as walking: the surface moved the
			// window, so the loop sees no movement of its own, but the
			// character should be running along with the pointer.
			dragging := d.petDragging && manual
			dragFacing := d.petDragFacing
			dragMovedAt := d.petDragMovedAt
			x, y = d.petX, d.petY
			geometry := d.petGeometry
			d.petMu.Unlock()

			// Watch spot on the main window's bottom edge; without one
			// the pet stays where it is.
			watchX, watchY := x, y
			if perchOK {
				watchX, watchY = petfeed.WatchSpot(
					geometry, mainRect, perchArea)
			}

			// Hover is polled here rather than reported by the surface:
			// the webview only delivers mouse movement while this window
			// is the key window of the active application, which an
			// always-on-top companion never is. Reading the global
			// pointer works while the user is in another app too.
			if cursorX, cursorY, ok := petfeed.CursorPosition(
				petWindowHandle(win),
			); ok {
				director.NoteHover(
					petfeed.CursorOnCharacter(
						cursorX, cursorY, x, y, geometry,
					),
					now,
				)
			}

			// The walk speed is a pack property: re-read it every
			// tick so switching character applies without restarting
			// the loop.
			walkSpeed := d.core.ActivePack().Meta.WalkSpeed

			// The pet only walks when it has somewhere to be: the watch
			// spot while the agent works or asks. Facing comes from the
			// loop's own step, never from the OS re-anchor in
			// keepPetOnScreen or a user drag: those deltas are larger
			// than one step and would flip the direction for a tick.
			walkFromX := x
			if petWalksToWatch(state.Disposition, perchOK, manual) {
				x = petStep(x, watchX, petLoopTick, walkSpeed)
				y = petStep(y, watchY, petLoopTick, walkSpeed)
			}
			facing = petFacing(facing, x-walkFromX)

			moved = false
			d.petMu.Lock()
			if x != d.petX || y != d.petY {
				d.petX, d.petY = x, y
				moved = true
			}
			d.petMu.Unlock()
			if moved {
				win.SetPosition(x, y)
			}

			walking := moved ||
				(dragging && !dragMovedAt.IsZero() &&
					now.Sub(dragMovedAt) < petDragWalkIdle)
			if dragging && dragFacing != "" {
				// The drag owns the facing while it lasts: the user is
				// walking the pet, and the next stale direction would
				// otherwise snap it back mid-gesture.
				facing = dragFacing
			}

			state = director.Tick(
				desktopcore.AssistantAgentID,
				now,
				d.core.Shell.LastUserActive(),
				walking,
			)
			debug := director.Debug(state)
			debug.Walking = walking
			d.petMu.Lock()
			d.petDebug = debug
			d.petMu.Unlock()

			payload := newPetStatePayload(state)
			payload.Walking = walking
			payload.Facing = facing
			payload.IntentSeq = intents.Observe(state.Intent)
			if payload == last {
				continue
			}
			last = payload
			app.Event.Emit(desktopcore.EventPetState, payload)
		}
	}
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

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// petWalksToWatch reports whether the pet should walk to its watch spot
// this tick. Only an agent at work or waiting for an answer gives the
// pet somewhere to be; idling and sleeping both mean standing still,
// and a user drag always wins.
func petWalksToWatch(
	disposition petfeed.PetDisposition, watchOK, manual bool,
) bool {
	if manual || !watchOK {
		return false
	}
	return disposition == petfeed.PetDispositionWork ||
		disposition == petfeed.PetDispositionAsk
}

// petStep moves current toward target by the distance travelled in one
// tick at speed DIP/s (the active pack's meta.walkSpeed), clamping at
// the target.
func petStep(current, target int, tick time.Duration, speed float64) int {
	step := int(speed * tick.Seconds())
	if step < 1 {
		step = 1
	}
	if current < target {
		current += step
		if current > target {
			return target
		}
		return current
	}
	if current > target {
		current -= step
		if current < target {
			return target
		}
	}
	return current
}

// Horizontal walk directions the rover reports. The vocabulary is
// deliberately tiny: the desktop pet only walks along the bottom edge,
// and packs translate these values into their own turn poses.
const (
	petFacingLeft  = "left"
	petFacingRight = "right"
)

// petFacing returns the walk direction for a horizontal step of dx,
// keeping prev when there was no sideways movement. dx is the rover's
// own step delta, so standing still, sleeping, OS re-anchoring and user
// drags all leave the last direction in place.
func petFacing(prev string, dx int) string {
	switch {
	case dx > 0:
		return petFacingRight
	case dx < 0:
		return petFacingLeft
	default:
		return prev
	}
}

// stopPet tears the pet window down during desktop shutdown.
func (d *Desktop) stopPet() {
	d.petMu.Lock()
	stop := d.petStop
	win := d.petWindow
	d.petStop = nil
	d.petWindow = nil
	d.petReady = false
	d.petDirector = nil
	d.petMu.Unlock()
	if stop != nil {
		close(stop)
	}
	if win != nil {
		win.Close()
	}
	// The report describes a window that is gone; diagnostics must not
	// keep presenting it as the live character.
	d.core.Shell.ClearPetRuntimeStatus()
	d.core.Shell.SetPetWindowControls(desktopcore.PetWindowControls{})
}

// petDiagnostics returns the latest rover/mind snapshot for bindings.
func (d *Desktop) petDiagnostics() petfeed.MindDebug {
	d.petMu.Lock()
	defer d.petMu.Unlock()
	return d.petDebug
}

// petWindowPosition returns the rover-tracked absolute position.
func (d *Desktop) petWindowPosition() (x, y int, ok bool) {
	d.petMu.Lock()
	defer d.petMu.Unlock()
	return d.petX, d.petY, d.petReady
}
