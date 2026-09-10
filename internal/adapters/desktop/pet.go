package desktop

import (
	"context"
	"math/rand"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	desktopcore "github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// assistantPetSize is the transparent window canvas around the roaming
// assistant pet. Keep it tight around the sprite: a transparent webview
// still captures clicks over its whole rectangle unless mouse events
// are ignored.
const assistantPetSize = 240

// petRoamTick is the rover physics step. The state broadcast runs on a
// slower cadence (see petStateBroadcastEvery).
const petRoamTick = 60 * time.Millisecond

// petManualHoldFor keeps autonomy paused after a user drag.
const petManualHoldFor = 5 * time.Second

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

// startAssistantPet creates the roaming assistant pet window when the
// desktop pet preference is enabled and starts the rover loop.
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
		Width:            assistantPetSize,
		Height:           assistantPetSize,
		MinWidth:         assistantPetSize,
		MinHeight:        assistantPetSize,
		MaxWidth:         assistantPetSize,
		MaxHeight:        assistantPetSize,
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
	// be dragged and clicked. Per-pixel click-through is a platform
	// follow-up; the window is small enough that the tradeoff is
	// acceptable for the roaming milestone.
	win.SetIgnoreMouseEvents(false)

	stop := make(chan struct{})
	director := petfeed.NewPetDirector(d.core.Pet)
	d.petMu.Lock()
	d.petWindow = win
	d.petStop = stop
	d.petDirector = director
	d.petMu.Unlock()
	d.core.Shell.SetPetWindowControls(
		d.movePetWindow,
		d.setPetWindowPosition,
		d.activatePet,
		d.pokePet,
		d.setRoamPaused,
		d.petDiagnostics,
		d.petWindowPosition,
	)

	go d.roamAssistantPet(ctx, stop, win, app, director)
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
	d.petX = x
	d.petY = y
	d.petManualUntil = time.Now().Add(petManualHoldFor)
	win.SetPosition(x, y)
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

// setRoamPaused freezes the autonomous rover until resumed.
func (d *Desktop) setRoamPaused(paused bool) {
	d.petMu.Lock()
	d.petRoamPaused = paused
	d.petMu.Unlock()
}

// dockAssistantPet parks the pet window at the bottom-right corner of
// the primary screen's work area and records the position on Desktop.
func (d *Desktop) dockAssistantPet(
	app *application.App, win *application.WebviewWindow,
) bool {
	screen := app.Screen.GetPrimary()
	if screen == nil {
		return false
	}
	area := screen.WorkArea
	x := area.X + area.Width - assistantPetSize - 16
	y := area.Y + area.Height - assistantPetSize - 8
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

// roamAssistantPet is the autonomous rover: it wanders to random
// positions while idle, freezes in place while the agent works or
// asks, sleeps after long inactivity, and yields to user drags.
func (d *Desktop) roamAssistantPet(
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

	screen := app.Screen.GetPrimary()
	if screen == nil {
		return
	}
	area := screen.WorkArea
	floorY := area.Y + area.Height - assistantPetSize - 8
	minTop := area.Y + 24
	minX := area.X + 16
	maxX := area.X + area.Width - assistantPetSize - 16

	targetX, targetY := x, y
	// Home is the pet's resting spot: it returns there after work and
	// only makes short, occasional strolls around it (A+B behavior).
	homeX, homeY := x, y
	nextStrollAt := time.Now().Add(3 * time.Second)
	mainX, mainY := x, y
	mainW, mainH := assistantPetSize, assistantPetSize
	perchMinX, perchMaxX := minX, maxX
	perchTop, perchFloor := minTop, floorY
	perchOK := false
	lastRectRefresh := time.Now().Add(-time.Second)
	wasManual := false
	ticker := time.NewTicker(petRoamTick)
	defer ticker.Stop()

	var last petStatePayload
	moved := false
	// facing carries the last walk direction across ticks; petFacing
	// keeps it when the pet does not move sideways.
	facing := ""
	var state petfeed.PetSurfaceState
	tickCount := 0
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
				main := d.core.Shell.MainWindow()
				if main != nil {
					mainX, mainY = main.Position()
					mainW, mainH = main.Size()
					perchOK = false
					centerX := mainX + mainW/2
					centerY := mainY + mainH/2
					for _, screen := range app.Screen.GetAll() {
						if screen == nil {
							continue
						}
						area := screen.WorkArea
						if centerX < area.X ||
							centerX >= area.X+area.Width ||
							centerY < area.Y ||
							centerY >= area.Y+area.Height {
							continue
						}
						perchMinX = area.X + 16
						perchMaxX = area.X + area.Width -
							assistantPetSize - 16
						perchTop = area.Y + 24
						perchFloor = area.Y + area.Height -
							assistantPetSize - 8
						perchOK = true
						minX = perchMinX
						maxX = perchMaxX
						minTop = perchTop
						floorY = perchFloor
						break
					}
				} else {
					perchOK = false
				}
			}
			d.petMu.Lock()
			manual := d.petRoamPaused || now.Before(d.petManualUntil)
			x, y = d.petX, d.petY
			d.petMu.Unlock()

			// Watch spot next to the main window; falls back to home
			// when the window has no room beside it.
			watchX, watchY := homeX, homeY
			if perchOK {
				watchY = mainY + mainH - assistantPetSize - 8
				watchY = clampInt(watchY, perchTop, perchFloor)
				rightX := mainX + mainW + 8
				leftX := mainX - assistantPetSize - 8
				switch {
				case rightX+assistantPetSize <= perchMaxX:
					watchX = rightX
				case leftX >= perchMinX:
					watchX = leftX
				default:
					watchX = homeX
				}
				watchX = clampInt(watchX, perchMinX, perchMaxX)
			}

			// The walk speed is a pack property: re-read it every
			// tick so switching character applies without restarting
			// the rover.
			roamSpeed := d.core.ActivePack().Meta.WalkSpeed

			// Re-anchor on the OS-reported position every few ticks.
			// Mixed-DPI displays can round window coordinates at the
			// boundary; following the OS instead of our accumulated
			// position prevents ±1px fighting there.
			tickCount++
			if tickCount%5 == 0 {
				osX, osY := win.Position()
				maxDelta := 3*int(roamSpeed*petRoamTick.Seconds()) + 8
				if absInt(x-osX) <= maxDelta && absInt(y-osY) <= maxDelta {
					x, y = osX, osY
				}
			}

			if wasManual && !manual {
				// The user parked the pet somewhere: that spot
				// becomes its new home.
				homeX = clampInt(x, minX, maxX)
				homeY = clampInt(y, minTop, floorY)
			}
			wasManual = manual

			// Facing comes from the rover's own step, never from the OS
			// re-anchor above or a user drag: those deltas are larger
			// than one step and would flip the direction for a tick.
			walkFromX := x
			if !manual {
				switch state.Disposition {
				case petfeed.PetDispositionSleep:
					// Asleep: stay put.
				case petfeed.PetDispositionRoam:
					if x == targetX && y == targetY {
						if now.After(nextStrollAt) {
							nextStrollAt = now.Add(petStrollDelay())
							targetX, targetY = petStrollTarget(
								homeX, homeY, minX, minTop, maxX, floorY)
						}
					} else if petDistance(x, y, homeX, homeY) >
						petStrollRange*2 {
						// Way off leash (drag, screen change): go home.
						targetX, targetY = homeX, homeY
					}
					x = petStep(x, targetX, petRoamTick, roamSpeed)
					y = petStep(y, targetY, petRoamTick, roamSpeed)
				default: // work / ask: walk to the window and stay there
					targetX, targetY = watchX, watchY
					x = petStep(x, targetX, petRoamTick, roamSpeed)
					y = petStep(y, targetY, petRoamTick, roamSpeed)
				}
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

			state = director.Tick(
				desktopcore.AssistantAgentID,
				now,
				d.core.Shell.LastUserActive(),
				moved,
			)
			debug := director.Debug(state)
			debug.Walking = moved
			d.petMu.Lock()
			d.petDebug = debug
			d.petMu.Unlock()

			payload := newPetStatePayload(state)
			payload.Walking = moved
			payload.Facing = facing
			payload.IntentSeq = intents.Observe(state.Intent)
			if payload == last {
				continue
			}
			last = payload
			app.Event.Emit("pet:state", payload)
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

// petStrollRange is how far (DIP) the pet may roam from home before it
// wanders back.
const petStrollRange = 360

// petStrollDelay is the pause between short strolls from home.
func petStrollDelay() time.Duration {
	return time.Duration(8+rand.Intn(13)) * time.Second
}

// petDistance is a cheap Manhattan distance used for home-leash checks.
func petDistance(x1, y1, x2, y2 int) int {
	return absInt(x1-x2) + absInt(y1-y2)
}

// petStrollTarget picks a short stroll point around home (with a chance
// to simply head home), clamped to the roamable area.
func petStrollTarget(
	homeX, homeY, xMin, yMin, xMax, yMax int,
) (int, int) {
	if rand.Intn(100) < 30 {
		return homeX, homeY
	}
	dx := rand.Intn(2*petStrollRange+1) - petStrollRange
	dy := rand.Intn(2*petStrollRange+1) - petStrollRange
	return clampInt(homeX+dx, xMin, xMax),
		clampInt(homeY+dy, yMin, yMax)
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
	d.core.Shell.SetPetWindowControls(
		nil, nil, nil, nil, nil, nil, nil)
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
