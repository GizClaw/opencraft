package pet

import (
	"testing"
	"time"
)

func TestMindWelcomesUserAfterAway(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()

	// First ever pulse: nothing to welcome.
	if got := mind.NoteUser(base, base); got != nil {
		t.Fatalf("first pulse must not welcome: %+v", got)
	}
	// Active pulses while the user stays around: no greeting.
	if got := mind.NoteUser(base.Add(2*time.Second), base.Add(2*time.Second)); got != nil {
		t.Fatalf("regular active pulse must not welcome: %+v", got)
	}
	// User returns after 30s away.
	back := base.Add(35 * time.Second)
	event := mind.NoteUser(back, back)
	if event == nil || event.Intent != PetIntentWelcome {
		t.Fatalf("return after away must welcome, got %+v", event)
	}
	// Repeated pulses inside the cooldown: no second greeting.
	if got := mind.NoteUser(back.Add(2*time.Second), back.Add(2*time.Second)); got != nil {
		t.Fatalf("cooldown must suppress repeat welcome: %+v", got)
	}
}

func TestMindIgnoresStalePulses(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	_ = mind.NoteUser(base, base)
	// An older pulse arriving late must not reset attention backwards.
	if got := mind.NoteUser(base.Add(10*time.Second), base.Add(1*time.Second)); got != nil {
		t.Fatalf("stale pulse must be ignored: %+v", got)
	}
}

func idleState() PetSurfaceState {
	return PetSurfaceState{
		AgentID:     "assistant",
		Phase:       PetPhaseIdle,
		Disposition: PetDispositionIdle,
	}
}

func TestMindSleepsWhenExhaustedAndWakes(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	mind.energy = 5
	state := mind.Step(idleState(), base, base, false)
	// Sleeping is a value (the director turns the sleep disposition into
	// the renderer's sleeping flag), not a fired reaction, so the mind
	// must not queue an intent for it.
	if state.Disposition != PetDispositionSleep || state.Intent != PetIntentNone {
		t.Fatalf("low energy must sleep without an intent, got %+v", state)
	}

	// Let energy regenerate above the wake threshold.
	now := base
	for i := 0; i < 80; i++ {
		now = now.Add(500 * time.Millisecond)
		state = mind.Step(state, now, now, false)
		if state.Disposition == PetDispositionIdle {
			return
		}
	}
	t.Fatalf("pet never woke up: drives=%+v", mind.Drives())
}

func TestMindZoomiesNeedsEnergyAndCooldown(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	mind.attention = 20
	mind.energy = zoomiesAt + 5
	state := mind.Step(idleState(), base, base, false)
	if state.Intent != PetIntentZoomies {
		t.Fatalf("high energy/low attention must zoom, got %+v", state)
	}
	if mind.energy >= zoomiesAt {
		t.Fatalf("zoomies must spend energy, energy=%v", mind.energy)
	}
	again := mind.Step(idleState(), base.Add(time.Minute), base.Add(time.Minute), false)
	if again.Intent == PetIntentZoomies {
		t.Fatal("zoomies must respect the cooldown")
	}
}

func TestMindPokeWakesAndWaves(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	mind.energy = 10
	mind.sleeping = true
	mind.NotePoke(base)
	if mind.energy < wakeAbove {
		t.Fatalf("poke must wake the pet, energy=%v", mind.energy)
	}
	state := mind.Step(idleState(), base.Add(time.Second), base.Add(time.Second), false)
	if state.Intent != PetIntentWave {
		t.Fatalf("poke must wave on the next tick, got %+v", state)
	}
	again := mind.Step(idleState(), base.Add(2*time.Second), base.Add(2*time.Second), false)
	if again.Intent == PetIntentWave {
		t.Fatal("wave must be one-shot")
	}
}

func TestMindHoverGreetsOnceThenLooks(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()

	// The first hover of a session is a greeting: wave on the next tick.
	mind.NoteHover(true, base)
	state := mind.Step(idleState(), base.Add(time.Second), base.Add(time.Second), false)
	if state.Intent != PetIntentWave {
		t.Fatalf("first hover must wave, got %+v", state)
	}
	// Leaving and returning immediately: nothing (still inside the
	// cooldown, and the greeting is spent).
	mind.NoteHover(false, base.Add(2*time.Second))
	mind.NoteHover(true, base.Add(2*time.Second))
	quiet := mind.Step(idleState(), base.Add(2*time.Second), base.Add(2*time.Second), false)
	if quiet.Intent != "" {
		t.Fatalf("hover inside the cooldown must stay quiet, got %+v", quiet)
	}
	// A hover after the cooldown is a glance.
	later := base.Add(hoverCooldown + 5*time.Second)
	mind.NoteHover(false, later)
	mind.NoteHover(true, later)
	look := mind.Step(idleState(), later.Add(time.Second), later.Add(time.Second), false)
	if look.Intent != PetIntentLook {
		t.Fatalf("hover after the cooldown must look, got %+v", look)
	}
	// Repeats of the same hover state are not transitions.
	mind.NoteHover(true, later.Add(2*time.Second))
	repeat := mind.Step(idleState(), later.Add(2*time.Second), later.Add(2*time.Second), false)
	if repeat.Intent != "" {
		t.Fatalf("a hover that never left must not react again, got %+v", repeat)
	}
}

func TestMindHoverLeavesASleepingPetAlone(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	mind.sleeping = true
	sleeping := idleState()
	sleeping.Disposition = PetDispositionSleep
	sleeping.Sleeping = true

	mind.NoteHover(true, base)
	state := mind.Step(sleeping, base.Add(time.Second), time.Time{}, false)
	if state.Intent != "" {
		t.Fatalf("hover must not react at a sleeping pet, got %+v", state)
	}
	if !mind.sleeping {
		t.Fatal("hover must not wake the pet")
	}
}

func TestMindHoverReactsWhileWorking(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	working := idleState()
	working.Disposition = PetDispositionWork
	working.Phase = PetPhaseTool

	mind.NoteHover(true, base)
	state := mind.Step(working, base.Add(time.Second), base.Add(time.Second), false)
	if state.Intent != PetIntentWave {
		t.Fatalf("hovering a working pet must greet, got %+v", state)
	}
	if state.Disposition != PetDispositionWork {
		t.Fatalf("the hover reaction must not change the disposition: %+v", state)
	}
}

func TestMindSulksAfterIgnoredDone(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	mind.comfort = 10
	done := PetSurfaceState{
		AgentID:     "assistant",
		Phase:       PetPhaseDone,
		Disposition: PetDispositionWork,
	}
	first := mind.Step(done, base, base, false)
	if first.Intent == PetIntentSulk {
		t.Fatal("sulk must wait until idle, not fire while working")
	}
	idle := mind.Step(idleState(), base.Add(3*time.Second), base.Add(3*time.Second), false)
	if idle.Intent != PetIntentSulk {
		t.Fatalf("ignored done must sulk when idle, got %+v", idle)
	}
}

func TestMindDoesNotSulkAfterRecentPoke(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	mind := NewMind()
	mind.comfort = 10
	mind.NotePoke(base)
	done := PetSurfaceState{
		AgentID:     "assistant",
		Phase:       PetPhaseDone,
		Disposition: PetDispositionWork,
	}
	_ = mind.Step(done, base.Add(30*time.Second), base.Add(30*time.Second), false)
	idle := mind.Step(
		idleState(),
		base.Add(40*time.Second),
		base.Add(40*time.Second),
		false,
	)
	if idle.Intent == PetIntentSulk {
		t.Fatal("recent poke must prevent sulking")
	}
}

func TestMindMoodLabels(t *testing.T) {
	mind := NewMind()
	if mind.Mood() != PetMoodContent {
		t.Fatalf("default mood = %q", mind.Mood())
	}
	mind.energy = 10
	if mind.Mood() != PetMoodSleepy {
		t.Fatalf("low energy mood = %q", mind.Mood())
	}
	mind.energy = 80
	mind.comfort = 10
	if mind.Mood() != PetMoodNeedy {
		t.Fatalf("low comfort mood = %q", mind.Mood())
	}
	mind.comfort = 60
	mind.attention = 80
	if mind.Mood() != PetMoodAttentive {
		t.Fatalf("high attention mood = %q", mind.Mood())
	}
	mind.attention = 20
	if mind.Mood() != PetMoodPlayful {
		t.Fatalf("high energy/low attention mood = %q", mind.Mood())
	}
}
