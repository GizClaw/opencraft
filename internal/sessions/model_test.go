package sessions

import "testing"

func TestModelDefaultAndRoundTrip(t *testing.T) {
	store, err := New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if model, err := store.Model("s-missing"); err != nil || model != "" {
		t.Errorf("Model(missing) = %q, %v; want \"\", nil", model, err)
	}
	if err := store.SetModel("s-x", "deepseek/deepseek-v4-flash"); err != nil {
		t.Fatal(err)
	}
	if model, err := store.Model("s-x"); err != nil || model != "deepseek/deepseek-v4-flash" {
		t.Errorf("Model(s-x) = %q, %v; want deepseek/deepseek-v4-flash, nil", model, err)
	}
	if err := store.SetModel("s-x", ""); err != nil {
		t.Fatal(err)
	}
	if model, err := store.Model("s-x"); err != nil || model != "" {
		t.Errorf("Model(s-x) after reset = %q, %v; want \"\", nil", model, err)
	}
}

func TestSetModelRejectsInvalidID(t *testing.T) {
	store, err := New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.SetModel("not-a-session", "openai/gpt-5.6-sol"); err == nil {
		t.Fatal("SetModel with invalid id should fail")
	}
}
