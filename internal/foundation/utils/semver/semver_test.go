package semver

import "testing"

// TestCompareOrdersSemverPrecedence is the rule both the plugin
// registry and the application registry gate on: a manifest's
// minHostVersion is read with this function, so the table is the
// definition, not an example.
func TestCompareOrdersSemverPrecedence(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"0.9.9", "1.0.0", -1},
		{"1.0.0", "1.0.0-beta", 1},
		{"1.0.0-beta", "1.0.0-rc.1", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		// Build metadata is parsed away, and a "v" prefix is a
		// spelling, not a different version.
		{"v1.2.3+build.7", "1.2.3", 0},
		{"1.2.3-rc.1+build.7", "1.2.3-rc.1", 0},
		// Surrounding whitespace is trimmed, like every other field a
		// YAML manifest carries.
		{" 2.0.0 ", "1.9.0", 1},
	}
	for _, tc := range cases {
		got, err := Compare(tc.a, tc.b)
		if err != nil {
			t.Fatalf("Compare(%q, %q): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if _, err := Compare("abc", "1.0.0"); err == nil {
		t.Fatal("an invalid version must be rejected instead of ordered")
	}
}

// TestValidRefusesWhatCompareWouldRefuse keeps the validation entry
// point and the comparison on one parser: a manifest version is
// accepted exactly when it can also be ordered.
func TestValidRefusesWhatCompareWouldRefuse(t *testing.T) {
	valid := []string{"0", "1", "0.1.0", "v1.2.3", "1.2.3-rc.1", "1.2.3+meta", "1.2.3-rc.1+meta"}
	for _, v := range valid {
		if err := Valid(v); err != nil {
			t.Errorf("Valid(%q) = %v, want nil", v, err)
		}
	}
	invalid := []string{
		"", " ", "abc", "1.2.3.4.5", "01.2.3", "1..2",
		"1.0.0-rc..1", "-1",
		"1.2.3-abcdefghijklmnopqrstuvwxyz0123456789",
		"1.2.3-rc.1.2.3.4.5.6.7.8.9",
	}
	for _, v := range invalid {
		if err := Valid(v); err == nil {
			t.Errorf("Valid(%q) = nil, want an error", v)
		}
	}
}
