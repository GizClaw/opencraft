package core

import (
	"os"
	"path/filepath"
	"testing"
)

// The renderer maps these ids onto CSS stacks (frontend/src/lib/appearance.ts).
// A rename on either side must fail here instead of silently dropping the
// user's font choice back to the system preset.
func TestUIPrefPresetIDsMatchTheRendererContract(t *testing.T) {
	wantFonts := []string{FontPresetSystem, FontPresetCustom}
	wantCode := []string{FontPresetSystem, FontPresetCustom}
	if len(uiFontPresetIDs) != len(wantFonts) {
		t.Fatalf("interface font presets = %v, want %v", uiFontPresetIDs, wantFonts)
	}
	for _, id := range wantFonts {
		if !uiFontPresetIDs[id] {
			t.Fatalf("interface font preset %q missing, want %v", id, wantFonts)
		}
	}
	if len(uiCodeFontPresetIDs) != len(wantCode) {
		t.Fatalf("code font presets = %v, want %v", uiCodeFontPresetIDs, wantCode)
	}
	for _, id := range wantCode {
		if !uiCodeFontPresetIDs[id] {
			t.Fatalf("code font preset %q missing, want %v", id, wantCode)
		}
	}
}

func TestDefaultPrefsCarryAppearanceDefaults(t *testing.T) {
	prefs := DefaultPrefs().UI
	if prefs.FontFamily != FontPresetSystem || prefs.CodeFont != FontPresetSystem {
		t.Fatalf("default appearance = %+v, want system fonts", prefs)
	}
	if prefs.FontScale != DefaultFontScale {
		t.Fatalf("default scale = %v, want %v", prefs.FontScale, DefaultFontScale)
	}

	// A preference document written before appearance settings existed
	// (or one that lost the section) still loads with defaults.
	loaded := LoadPrefs(t.TempDir()).UI
	if loaded != defaultUIPrefs() {
		t.Fatalf("appearance from a fresh document = %+v, want %+v",
			loaded, defaultUIPrefs())
	}
}

func TestLoadPrefsRepairsAppearanceSection(t *testing.T) {
	dir := t.TempDir()
	data := `{
	  "closeToTray": true,
	  "ui": {
	    "fontFamily": "comic-sans",
	    "codeFont": "custom",
	    "codeFontName": "  Fira   Code ;  ",
	    "fontScale": 9
	  }
	}`
	if err := os.WriteFile(
		filepath.Join(dir, prefsFile), []byte(data), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	ui := LoadPrefs(dir).UI
	if ui.FontFamily != FontPresetSystem {
		t.Fatalf("unknown interface font survived as %q", ui.FontFamily)
	}
	if ui.CodeFont != FontPresetCustom {
		t.Fatalf("code font = %q, want custom", ui.CodeFont)
	}
	if ui.CodeFontName != "Fira Code" {
		t.Fatalf("code font name = %q, want %q", ui.CodeFontName, "Fira Code")
	}
	if ui.FontScale != maxFontScale {
		t.Fatalf("scale = %v, want clamped to %v", ui.FontScale, maxFontScale)
	}
}

// A "custom" selection without a usable stack must not leave the renderer
// without a font: it falls back to the system preset.
func TestNormalizeUIPrefsDropsEmptyCustomSelection(t *testing.T) {
	ui := normalizeUIPrefs(UIPrefs{
		FontFamily: FontPresetCustom,
		CodeFont:   FontPresetCustom,
		FontScale:  minFontScale / 2,
	})
	if ui.FontFamily != FontPresetSystem || ui.CodeFont != FontPresetSystem {
		t.Fatalf("empty custom selection survived as %+v", ui)
	}
	if ui.FontScale != minFontScale {
		t.Fatalf("scale = %v, want clamped to %v", ui.FontScale, minFontScale)
	}
}

func TestSanitizeFontNameDropsStylePunctuation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "family names keep their spacing",
			raw:  "  LXGW   WenKai  ",
			want: "LXGW WenKai",
		},
		{
			name: "control characters and newlines collapse",
			raw:  "Inter\n\tSans ",
			want: "Inter Sans",
		},
		{
			name: "declaration breakers are dropped",
			raw:  "Foo; color: red } body { <x> !important",
			want: "Foo color: red body x important",
		},
		{
			name: "empty stays empty",
			raw:  "   ",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeFontName(tc.raw); got != tc.want {
				t.Fatalf("sanitizeFontName(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestShellUISettingsPersistAndValidate(t *testing.T) {
	dir := t.TempDir()
	s := NewShell(dir)
	if got := s.UISettings(); got != defaultUIPrefs() {
		t.Fatalf("fresh shell appearance = %+v, want %+v", got, defaultUIPrefs())
	}

	want := UIPrefs{
		FontFamily:     FontPresetCustom,
		FontFamilyName: "LXGW WenKai",
		CodeFont:       FontPresetSystem,
		FontScale:      1.25,
	}
	if err := s.SetUISettings(want); err != nil {
		t.Fatal(err)
	}
	if got := NewShell(dir).UISettings(); got != want {
		t.Fatalf("reloaded appearance = %+v, want %+v", got, want)
	}

	// A renderer/desktop preset mismatch and an out-of-range scale are
	// rejected without touching the stored document.
	bad := want
	bad.FontFamily = "comic-sans"
	if err := s.SetUISettings(bad); err == nil {
		t.Fatal("unknown interface font preset was accepted")
	}
	bad = want
	bad.CodeFont = "comic-mono"
	if err := s.SetUISettings(bad); err == nil {
		t.Fatal("unknown code font preset was accepted")
	}
	bad = want
	bad.FontScale = maxFontScale + 0.5
	if err := s.SetUISettings(bad); err == nil {
		t.Fatal("out-of-range font scale was accepted")
	}
	if got := s.UISettings(); got != want {
		t.Fatalf("rejected writes changed the document: %+v", got)
	}

	// Values inside the supported range are normalized, not rejected.
	loose := want
	loose.FontScale = 1.2345
	loose.FontFamilyName = "  LXGW   WenKai  "
	if err := s.SetUISettings(loose); err != nil {
		t.Fatal(err)
	}
	got := NewShell(dir).UISettings()
	if got.FontScale != 1.23 {
		t.Fatalf("scale = %v, want 1.23", got.FontScale)
	}
	if got.FontFamilyName != "LXGW WenKai" {
		t.Fatalf("family name = %q, want collapsed whitespace", got.FontFamilyName)
	}
}
