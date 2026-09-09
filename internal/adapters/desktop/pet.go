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

// petRoamSpeed is the horizontal walking speed in DIP/s.
const petRoamSpeed = 110

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
	Intent       string `json:"intent,omitempty"`
	Bubble       string `json:"bubble,omitempty"`
}

func newPetStatePayload(st petfeed.PetSurfaceState) petStatePayload {
	p := petStatePayload{
		AgentID:     st.AgentID,
		Phase:       string(st.Phase),
		Disposition: string(st.Disposition),
		Interactive: st.Interactive,
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
	nextTargetAt := time.Now().Add(petRoamDelay())
	// Habitat tracking: while the main window is visible and the user
	// is near, the pet perches on the window edge and follows it.
	mainX, mainY := x, y
	mainW, mainH := assistantPetSize, assistantPetSize
	mainVisible := false
	perchMinX, perchMaxX := minX, maxX
	perchTop, perchFloor := minTop, floorY
	perchOK := false
	lastRectRefresh := time.Now().Add(-time.Second)
	wasAttached := false
	ticker := time.NewTicker(petRoamTick)
	defer ticker.Stop()

	var last petStatePayload
	moved := false
	var state petfeed.PetSurfaceState
	tickCount := 0
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
					mainVisible = main.IsVisible() && !main.IsMinimised()
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
					mainVisible = false
					perchOK = false
				}
			}
			lastUser := d.core.Shell.LastUserActive()
			userNear := !lastUser.IsZero() &&
				now.Sub(lastUser) <= 10*time.Second
			habitat := mainVisible && perchOK && userNear

			d.petMu.Lock()
			manual := d.petRoamPaused || now.Before(d.petManualUntil)
			x, y = d.petX, d.petY
			d.petMu.Unlock()

			// Re-anchor on the OS-reported position every few ticks.
			// Mixed-DPI displays can round window coordinates at the
			// boundary; following the OS instead of our accumulated
			// position prevents ±1px fighting there.
			tickCount++
			if tickCount%5 == 0 {
				osX, osY := win.Position()
				maxDelta := 3*int(float64(petRoamSpeed)*
					petRoamTick.Seconds()) + 8
				if absInt(x-osX) <= maxDelta && absInt(y-osY) <= maxDelta {
					x, y = osX, osY
				}
			}

			if !manual {
				switch state.Disposition {
				case petfeed.PetDispositionSleep:
					// Asleep: stay put.
				case petfeed.PetDispositionRoam:
					if habitat {
						// Perch on the outside of the main window's
						// bottom-right corner: right side first, left
						// side when the window hugs the screen edge.
						targetY = mainY + mainH - assistantPetSize - 8
						targetY = clampInt(targetY, perchTop, perchFloor)
						rightX := mainX + mainW + 8
						leftX := mainX - assistantPetSize - 8
						rightFits := rightX+assistantPetSize <= perchMaxX
						leftFits := leftX >= perchMinX
						switch {
						case rightFits:
							targetX = rightX
						case leftFits:
							targetX = leftX
						default:
							// No perch beside the window on this screen:
							// stop in place instead of jumping to a far
							// corner or another monitor.
							if !wasAttached {
								wasAttached = true
								targetX, targetY = x, y
							}
						}
						targetX = clampInt(targetX, perchMinX, perchMaxX)
						if !wasAttached {
							wasAttached = true
						}
					} else {
						if wasAttached {
							// Leaving the habitat: pause on the spot
							// before picking a fresh wander target.
							wasAttached = false
							targetX, targetY = x, y
							nextTargetAt = now.Add(petRoamDelay())
						}
						if x == targetX && y == targetY &&
							now.After(nextTargetAt) {
							spanX := maxX - minX
							spanY := floorY - minTop
							if spanX > 0 {
								targetX = minX + rand.Intn(spanX+1)
							}
							if spanY > 0 {
								targetY = minTop + rand.Intn(spanY+1)
							}
							nextTargetAt = now.Add(petRoamDelay())
						}
					}
					x = petStep(x, targetX, petRoamTick)
					y = petStep(y, targetY, petRoamTick)
				default: // work / ask: stop in place and act
					if x != targetX || y != targetY {
						targetX, targetY = x, y
						nextTargetAt = now.Add(petRoamDelay())
					}
				}
			}

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

// petRoamDelay returns the pause between idle wander targets, randomized
// between 3 and 10 seconds so movement does not feel mechanical.
func petRoamDelay() time.Duration {
	return time.Duration(3+rand.Intn(8)) * time.Second
}

// petStep moves current toward target by the distance travelled in one
// tick at petRoamSpeed, clamping at the target.
func petStep(current, target int, tick time.Duration) int {
	step := int(float64(petRoamSpeed) * tick.Seconds())
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
