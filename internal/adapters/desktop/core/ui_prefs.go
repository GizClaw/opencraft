package core

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Desktop interface preferences (Settings > Interface): the interface font,
// the code/mono font, the whole-UI scale, the accent colour and the
// workspace tree's hidden-file switch.
//
// A font is stored as a preset id plus, for the "custom" preset, the family
// name to render with. The catalogue of names comes from the host
// (internal/foundation/platform/sysfont), while the renderer owns how a family becomes
// a CSS stack with the platform fallback appended — so only the preset ids
// are a contract with frontend/src/lib/appearance.ts. Keep both sides in
// sync.

const (
	// DefaultFontScale keeps today's look: the 14px design base scaled by
	// 1.12 (see frontend/src/style.css).
	DefaultFontScale = 1.12
	minFontScale     = 0.85
	maxFontScale     = 1.60
	// maxFontNameRunes caps one font family name.
	maxFontNameRunes = 120
)

const (
	// FontPresetSystem is the platform UI font stack; it is also the fallback
	// for unknown or unusable declarations.
	FontPresetSystem = "system"
	// FontPresetCustom renders the named family from the system catalogue.
	FontPresetCustom = "custom"
)

var uiFontPresetIDs = map[string]bool{
	FontPresetSystem: true,
	FontPresetCustom: true,
}

var uiCodeFontPresetIDs = map[string]bool{
	FontPresetSystem: true,
	FontPresetCustom: true,
}

// DefaultAccent is the accent preset a document starts on; the ids are the
// renderer/desktop contract (frontend/src/lib/appearance.ts keeps the same
// set, and style.css draws each one's rungs).
const DefaultAccent = "blue"

var uiAccentIDs = map[string]bool{
	"blue":   true,
	"violet": true,
	"teal":   true,
	"orange": true,
	"rose":   true,
}

// UIPrefs is the persisted interface section of the desktop preference
// document. The family names are only meaningful while their selection is
// "custom".
type UIPrefs struct {
	FontFamily     string  `json:"fontFamily,omitempty"`
	FontFamilyName string  `json:"fontFamilyName,omitempty"`
	CodeFont       string  `json:"codeFont,omitempty"`
	CodeFontName   string  `json:"codeFontName,omitempty"`
	FontScale      float64 `json:"fontScale,omitempty"`
	// Accent is the highlight-colour preset id (blue/violet/teal/orange/
	// rose, see uiAccentIDs).
	Accent string `json:"accent,omitempty"`
	// ShowHiddenFiles lists dot-entries in the chat rail's workspace
	// tree and quick-open search. Off by default: a fresh workspace
	// reads as its tracked content, not as .git and editor litter.
	ShowHiddenFiles bool `json:"showHiddenFiles,omitempty"`
	// GitMarks draws the per-line git change marks in the file viewer.
	// It is a pointer because the switch defaults to ON: an absent
	// field (every document written before the marks existed) means
	// enabled, and only an explicit false turns the marks off.
	GitMarks *bool `json:"gitMarks,omitempty"`
}

// defaultUIPrefs returns the appearance defaults written on first run.
func defaultUIPrefs() UIPrefs {
	return UIPrefs{
		FontFamily: FontPresetSystem,
		CodeFont:   FontPresetSystem,
		FontScale:  DefaultFontScale,
		Accent:     DefaultAccent,
	}
}

// normalizeUIPrefs repairs an appearance section that came from disk or from
// a caller: unknown preset ids fall back to the system preset, a "custom"
// selection without a usable stack falls back too, and the scale is clamped
// into the supported range. Loading tolerates hand-edited files instead of
// rejecting them so a broken preference cannot lock the user out of the UI.
func normalizeUIPrefs(prefs UIPrefs) UIPrefs {
	defaults := defaultUIPrefs()
	if !uiFontPresetIDs[prefs.FontFamily] {
		prefs.FontFamily = defaults.FontFamily
	}
	if !uiCodeFontPresetIDs[prefs.CodeFont] {
		prefs.CodeFont = defaults.CodeFont
	}
	prefs.FontFamilyName = sanitizeFontName(prefs.FontFamilyName)
	prefs.CodeFontName = sanitizeFontName(prefs.CodeFontName)
	if prefs.FontFamily == FontPresetCustom && prefs.FontFamilyName == "" {
		prefs.FontFamily = defaults.FontFamily
	}
	if prefs.CodeFont == FontPresetCustom && prefs.CodeFontName == "" {
		prefs.CodeFont = defaults.CodeFont
	}
	prefs.FontScale = normalizeFontScale(prefs.FontScale)
	if !uiAccentIDs[prefs.Accent] {
		prefs.Accent = defaults.Accent
	}
	return prefs
}

// validateUIPrefs rejects the values the renderer must never send. Preset ids
// are the renderer/desktop contract, so a mismatch surfaces as a save error
// instead of being silently rewritten; everything else is normalized. Family
// names are not checked against the catalogue: a name can come from a font
// installed after the list was read, and an unknown family degrades into the
// platform fallback in CSS.
func validateUIPrefs(prefs UIPrefs) error {
	if !uiFontPresetIDs[prefs.FontFamily] {
		return fmt.Errorf("ui prefs: unknown interface font preset %q", prefs.FontFamily)
	}
	if !uiCodeFontPresetIDs[prefs.CodeFont] {
		return fmt.Errorf("ui prefs: unknown code font preset %q", prefs.CodeFont)
	}
	if math.IsNaN(prefs.FontScale) ||
		prefs.FontScale < minFontScale ||
		prefs.FontScale > maxFontScale {
		return fmt.Errorf(
			"ui prefs: font scale %v outside [%v, %v]",
			prefs.FontScale, minFontScale, maxFontScale,
		)
	}
	if !uiAccentIDs[prefs.Accent] {
		return fmt.Errorf("ui prefs: unknown accent preset %q", prefs.Accent)
	}
	return nil
}

// normalizeFontScale clamps a scale into the supported range, rounding to two
// decimals so stored documents stay readable. Missing values (0) keep the
// default.
func normalizeFontScale(scale float64) float64 {
	if math.IsNaN(scale) || scale <= 0 {
		return DefaultFontScale
	}
	clamped := math.Min(math.Max(scale, minFontScale), maxFontScale)
	return math.Round(clamped*100) / 100
}

// sanitizeFontName trims and bounds one family name, dropping control
// characters and the punctuation that would break out of the CSS declaration
// the name is fed into. Quotes, commas and spaces survive because real family
// names use them.
func sanitizeFontName(raw string) string {
	if raw == "" {
		return ""
	}
	var b strings.Builder
	written := 0
	pendingSpace := false
	for _, r := range raw {
		if written >= maxFontNameRunes {
			break
		}
		if unicode.IsSpace(r) {
			pendingSpace = b.Len() > 0
			continue
		}
		if dropFontNameRune(r) {
			continue
		}
		if pendingSpace {
			b.WriteRune(' ')
			written++
			pendingSpace = false
		}
		b.WriteRune(r)
		written++
	}
	return b.String()
}

// dropFontNameRune reports whether a rune must not reach the preference
// document: control characters plus the punctuation that ends a CSS
// declaration or opens a markup context.
func dropFontNameRune(r rune) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	switch r {
	case ';', '{', '}', '<', '>', '\\', '`', '!':
		return true
	default:
		return false
	}
}
