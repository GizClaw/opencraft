package sysfont

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestFilterFamiliesDropsPrivateFacesAndDuplicates(t *testing.T) {
	got := filterFamilies([]string{
		" Helvetica ",
		"helvetica",
		".SF Pro Text", // macOS private face: not user-selectable
		"",
		"Arial",
		"  ",
		"Arial",
		"Noto Sans CJK SC",
	})
	want := []string{"Arial", "Helvetica", "Noto Sans CJK SC"}
	if !slices.Equal(got, want) {
		t.Fatalf("filterFamilies() = %v, want %v", got, want)
	}
}

// Two kinds of catalogue entry are not selectable interface fonts: the
// vertical-writing variants GDI adds on Windows, and faces that draw icon or
// emoji artwork instead of text. Coding fonts that merely ship Nerd Font
// glyphs, and system text faces with similar names, must survive.
func TestFilterFamiliesDropsVerticalAndIconOnlyFaces(t *testing.T) {
	got := filterFamilies([]string{
		"@Microsoft YaHei",
		"@SimSun",
		"Apple Color Emoji",
		"Apple Symbols",
		"Bootstrap Icons",
		"Font Awesome 6 Free",
		"Google Symbols",
		"Material Icons Outlined",
		"Material Symbols Rounded",
		"Noto Color Emoji",
		"Octicons Regular",
		"Segoe Fluent Icons",
		"Segoe MDL2 Assets",
		"Segoe UI Emoji",
		"Segoe UI Symbol",
		"Symbols Nerd Font",
		"Symbols Nerd Font Mono",
		"Weather Icons",
		"0xProto Nerd Font",
		"0xProto Nerd Font Mono",
		"0xProto Nerd Font Propo",
		"Maple Mono NF CN",
		"JetBrainsMono Nerd Font",
		"Segoe UI Variable",
		"Noto Sans SC",
		"Sego Symbols Sans",
	})
	want := []string{
		"0xProto Nerd Font",
		"0xProto Nerd Font Mono",
		"0xProto Nerd Font Propo",
		"JetBrainsMono Nerd Font",
		"Maple Mono NF CN",
		"Noto Sans SC",
		"Sego Symbols Sans",
		"Segoe UI Variable",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("filterFamilies() = %v, want %v", got, want)
	}
}

func TestListReturnsSortedUniqueCatalogue(t *testing.T) {
	families, err := List()
	if err != nil && len(families) == 0 {
		// A headless build host may have no font store at all (no fontconfig
		// on a minimal image); the desktop settings page treats that as
		// "type a family name" rather than an error.
		t.Skipf("host exposes no font catalogue: %v", err)
	}
	// macOS always ships families, so an empty list there means the CoreText
	// implementation broke rather than "no fonts installed".
	if len(families) == 0 && runtime.GOOS == "darwin" {
		t.Fatal("List() returned no families on darwin")
	}
	if len(families) == 0 {
		return
	}
	if !slices.IsSortedFunc(families, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	}) {
		t.Fatalf("List() is not sorted: %v", families)
	}
	seen := make(map[string]bool, len(families))
	for _, family := range families {
		key := strings.ToLower(family)
		if seen[key] {
			t.Fatalf("List() repeats %q", family)
		}
		seen[key] = true
		if strings.HasPrefix(family, ".") {
			t.Fatalf("List() includes private face %q", family)
		}
	}

	// The cached copy is a snapshot: mutating it must not corrupt later
	// callers.
	families[0] = "mutated"
	again, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if again[0] == "mutated" {
		t.Fatal("List() handed out the cached slice")
	}
}
