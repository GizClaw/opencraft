package pet

import "time"

// PetIntent is a semantic micro-interaction the renderer should play.
// Intent names map to optional Rive bindings ("intent:<name>"); when a
// pack has no binding the intent degrades to a bubble only.
type PetIntent string

const (
	PetIntentNone    PetIntent = ""
	PetIntentWelcome PetIntent = "welcome"
	PetIntentLook    PetIntent = "look"
	PetIntentZoomies PetIntent = "zoomies"
	PetIntentWave    PetIntent = "wave"
	PetIntentSulk    PetIntent = "sulk"
)

// MindEvent is one personality reaction decided by the Mind.
type MindEvent struct {
	Intent PetIntent
	// Bubble carries optional copy rendered verbatim on the surface.
	Bubble string
}

// DriveSnapshot reports the current internal drives for diagnostics and
// tuning. Values are bounded to [0,100].
type DriveSnapshot struct {
	Attention float64 `json:"attention"`
	Energy    float64 `json:"energy"`
	Comfort   float64 `json:"comfort"`
}

// Mind is the pet's lightweight personality state. It tracks drives
// (attention/energy/comfort), recent user attention, and decides both
// one-shot reactions (welcome, look, wave, sulk, zoomies) and behavior
// overrides (sleep when exhausted or left alone). Sleeping is a value
// the renderer writes, not a fired reaction, so it is not in the intent
// vocabulary. Short-term
// interaction memory plugs into the same type later without touching
// the activity feed.
type Mind struct {
	lastUserActive time.Time
	lastWelcome    time.Time
	lastDrive      time.Time
	lastZoomies    time.Time
	lastPoke       time.Time
	lastSulk       time.Time
	lastLook       time.Time
	pendingWave    bool
	pendingSulk    bool
	pokeCount      int

	attention   float64
	energy      float64
	comfort     float64
	sleeping    bool
	energySleep bool
	pokeStreak  int
}

// NewMind creates an empty mind with neutral drives.
func NewMind() *Mind {
	return &Mind{
		attention: 50,
		energy:    80,
		comfort:   60,
	}
}

// Drive bounds.
const (
	driveMin    = 0.0
	driveMax    = 100.0
	sleepBelow  = 18.0
	wakeAbove   = 45.0
	zoomiesAt   = 70.0
	zoomiesWait = 2 * time.Minute
	// lookMinGap is the smallest pulse gap that reads as a glance at
	// the pet; lookCooldown stops the pet from reacting to every pulse.
	lookMinGap   = time.Second
	lookCooldown = 45 * time.Second
)

// petUserNearAfter is how recent a user pulse (or poke) has to be for
// the pet to treat the user as present.
const petUserNearAfter = 10 * time.Second

// Drives returns the current drive values.
func (m *Mind) Drives() DriveSnapshot {
	return DriveSnapshot{
		Attention: m.attention,
		Energy:    m.energy,
		Comfort:   m.comfort,
	}
}

// PetMood is a coarse label derived from the drives, useful for
// diagnostics and for the renderer to pick idle flavor.
type PetMood string

const (
	PetMoodContent   PetMood = "content"
	PetMoodSleepy    PetMood = "sleepy"
	PetMoodNeedy     PetMood = "needy"
	PetMoodAttentive PetMood = "attentive"
	PetMoodPlayful   PetMood = "playful"
)

// Mood derives one coarse label from the current drives.
func (m *Mind) Mood() PetMood {
	switch {
	case m.energy < 20:
		return PetMoodSleepy
	case m.comfort < 25:
		return PetMoodNeedy
	case m.attention >= 60 && m.energy > 45:
		return PetMoodAttentive
	case m.energy > 70 && m.attention < 40:
		return PetMoodPlayful
	default:
		return PetMoodContent
	}
}

// MindStats summarizes interaction history for diagnostics and future
// memory tuning.
type MindStats struct {
	PokeCount int       `json:"poke_count"`
	LastPoke  time.Time `json:"last_poke,omitempty"`
	LastSulk  time.Time `json:"last_sulk,omitempty"`
}

// MindDebug is the wire snapshot shown in the settings diagnostics
// panel: current drives, mood, interaction stats and the rover state
// that produced them.
type MindDebug struct {
	Drives      DriveSnapshot  `json:"drives"`
	Mood        PetMood        `json:"mood"`
	Stats       MindStats      `json:"stats"`
	Disposition PetDisposition `json:"disposition"`
	Phase       PetPhase       `json:"phase"`
	Walking     bool           `json:"walking"`
}

// Stats returns interaction counters.
func (m *Mind) Stats() MindStats {
	return MindStats{
		PokeCount: m.pokeCount,
		LastPoke:  m.lastPoke,
		LastSulk:  m.lastSulk,
	}
}

// Debug snapshots the mind-only portion of the diagnostics payload.
func (m *Mind) Debug() MindDebug {
	return MindDebug{
		Drives: m.Drives(),
		Mood:   m.Mood(),
		Stats:  m.Stats(),
	}
}

// NotePoke records an interaction with the pet (click/pet). It feeds
// comfort back up, wakes a sleeping pet, and queues a wave reaction.
func (m *Mind) NotePoke(now time.Time) {
	if !m.lastPoke.IsZero() && now.Sub(m.lastPoke) <= 1500*time.Millisecond {
		m.pokeStreak++
	} else {
		m.pokeStreak = 1
	}
	m.lastPoke = now
	m.pokeCount++
	m.comfort = clampDrive(m.comfort + 5)
	m.attention = clampDrive(m.attention + 15)
	m.pendingWave = true
	if m.sleeping {
		m.sleeping = false
		m.energySleep = false
		if m.energy < wakeAbove {
			m.energy = wakeAbove
		}
	}
}

// awayWelcomeAfter is how long the user must be away before a return
// counts as "coming back" instead of an ordinary active pulse.
const awayWelcomeAfter = 5 * time.Second

// welcomeCooldown prevents the pet from greeting on every attention
// pulse right after a return.
const welcomeCooldown = 90 * time.Second

// NoteUser folds one user-activity report into the mind. lastUserActive
// is the timestamp of the latest main-window activity pulse. It returns
// a reaction when the user returns after being away long enough.
func (m *Mind) NoteUser(now, lastUserActive time.Time) *MindEvent {
	if lastUserActive.IsZero() || lastUserActive.Before(m.lastUserActive) {
		return nil
	}
	away := lastUserActive.Sub(m.lastUserActive)
	wasAway := !m.lastUserActive.IsZero() && away >= awayWelcomeAfter
	m.lastUserActive = lastUserActive
	if !wasAway || now.Sub(m.lastWelcome) < welcomeCooldown {
		return nil
	}
	m.lastWelcome = now
	return &MindEvent{Intent: PetIntentWelcome}
}

// Step folds one rover tick into the drives and returns the state with
// any behavior override applied. moving reports whether the window
// physically moved this tick; lastUserActive drives attention.
func (m *Mind) Step(
	state PetSurfaceState,
	now, lastUserActive time.Time,
	moving bool,
) PetSurfaceState {
	if !m.lastDrive.IsZero() {
		dt := now.Sub(m.lastDrive)
		if dt < 0 {
			dt = 0
		}
		if dt > 500*time.Millisecond {
			dt = 500 * time.Millisecond
		}
		secs := dt.Seconds()

		// Attention follows the user: climbs while they are near,
		// decays while they are away.
		if !lastUserActive.IsZero() &&
			now.Sub(lastUserActive) <= 5*time.Second {
			m.attention = clampDrive(m.attention + 30*secs)
		} else {
			m.attention = clampDrive(m.attention - 2.5*secs)
		}

		// Energy drains while walking and regenerates at rest.
		if moving {
			m.energy = clampDrive(m.energy - 2*secs)
		} else {
			m.energy = clampDrive(m.energy + 1.2*secs)
		}
		m.comfort = clampDrive(m.comfort - 0.5*secs)
	}
	m.lastDrive = now

	// A completed turn the user ignores makes the pet sulk later, once
	// it is back to idling (not while it is still "working" on done).
	if state.Phase == PetPhaseDone &&
		m.comfort < 30 &&
		ignoredAfterDone(m.lastPoke, m.lastSulk, now) &&
		now.Sub(m.lastSulk) >= 3*time.Minute {
		m.lastSulk = now
		m.pendingSulk = true
	}
	if state.Disposition == PetDispositionWork ||
		state.Disposition == PetDispositionAsk {
		m.sleeping = false
		m.energySleep = false
		return state
	}

	userNear := now.Sub(m.lastPoke) <= petUserNearAfter
	if !lastUserActive.IsZero() {
		userNear = userNear || now.Sub(lastUserActive) <= petUserNearAfter
	}

	// Long feed silence is only a nap candidate when the user is gone;
	// a pet that sleeps while someone is typing nearby feels broken.
	// Inactivity naps ignore energy and last until activity, a poke or
	// the user coming back.
	if state.Disposition == PetDispositionSleep && !m.energySleep {
		switch {
		case userNear:
			m.sleeping = false
			state.Disposition = PetDispositionRoam
		case !m.sleeping:
			m.sleeping = true
		}
	}

	// The drive selector only reshapes idle behavior; active work
	// always wins above it.
	switch {
	case m.energy <= sleepBelow && !m.sleeping:
		m.sleeping = true
		m.energySleep = true
		state.Disposition = PetDispositionSleep
	case m.sleeping && m.energy >= wakeAbove && m.energySleep:
		m.sleeping = false
		m.energySleep = false
		state.Disposition = PetDispositionRoam
	case m.sleeping:
		state.Disposition = PetDispositionSleep
	case state.Disposition == PetDispositionRoam &&
		m.energy >= zoomiesAt &&
		m.attention < 40 &&
		now.Sub(m.lastZoomies) >= zoomiesWait:
		m.lastZoomies = now
		m.energy = clampDrive(m.energy - 20)
		state.Intent = PetIntentZoomies
	}

	// A pulse after a short absence reads as the user glancing over:
	// the pet looks back. Long absences become a welcome via NoteUser.
	freshPulse := !m.lastUserActive.IsZero() &&
		!lastUserActive.IsZero() &&
		lastUserActive.After(m.lastUserActive)
	if freshPulse && state.Intent == "" && !m.sleeping {
		away := lastUserActive.Sub(m.lastUserActive)
		if state.Disposition == PetDispositionRoam &&
			away >= lookMinGap &&
			away < awayWelcomeAfter &&
			now.Sub(m.lastLook) >= lookCooldown {
			m.lastLook = now
			state.Intent = PetIntentLook
		}
	}

	// Poke reactions only interrupt idle/roam states, never work.
	if m.pendingWave &&
		state.Disposition != PetDispositionWork &&
		state.Disposition != PetDispositionAsk {
		m.pendingWave = false
		state.Intent = PetIntentWave
	}

	if m.pendingSulk &&
		state.Intent == "" &&
		state.Disposition != PetDispositionSleep &&
		state.Disposition != PetDispositionWork &&
		state.Disposition != PetDispositionAsk {
		m.pendingSulk = false
		m.comfort = clampDrive(m.comfort + 15)
		state.Intent = PetIntentSulk
	}
	return state
}

// ignoredAfterDone reports whether the user has not poked the pet for a
// while (or ever) after the last completed turn, so sulking only follows
// genuine neglect instead of a recent interaction.
func ignoredAfterDone(lastPoke, lastSulk, now time.Time) bool {
	if lastPoke.IsZero() {
		return true
	}
	if now.Sub(lastPoke) <= 2*time.Minute {
		return false
	}
	return !lastSulk.After(lastPoke)
}

func clampDrive(value float64) float64 {
	if value < driveMin {
		return driveMin
	}
	if value > driveMax {
		return driveMax
	}
	return value
}
