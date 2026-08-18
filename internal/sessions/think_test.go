package sessions

import "testing"

func TestThinkDefaultAndRoundTrip(t *testing.T) {
	store, err := New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if level, err := store.Think("s-missing"); err != nil || level != ThinkMedium {
		t.Errorf("Think(missing) = %q, %v; want medium, nil", level, err)
	}
	if err := store.SetThink("s-x", ThinkHigh); err != nil {
		t.Fatal(err)
	}
	if level, err := store.Think("s-x"); err != nil || level != ThinkHigh {
		t.Errorf("Think(s-x) = %q, %v; want high, nil", level, err)
	}
}

func TestSetThinkRejectsInvalid(t *testing.T) {
	store, err := New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetThink("s-x", ThinkLevel("max")); err == nil {
		t.Fatal("SetThink(max) should fail")
	}
}
