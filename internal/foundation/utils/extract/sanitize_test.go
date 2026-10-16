package extract

import (
	"strings"
	"testing"
)

// TestHiddenStyleMatrix is the coverage matrix for the hiding rules:
// one case per way a stylesheet can hide content, each written in a
// spelling that a substring test over the raw attribute misses.
func TestHiddenStyleMatrix(t *testing.T) {
	cases := []struct {
		name  string
		style string
	}{
		{"display none", "display:none"},
		{"display none, spaced", "display : none"},
		{"display none, important", "display:none !important"},
		{"display none, upper case", "DISPLAY:NONE"},
		{"visibility hidden", "visibility:hidden"},
		{"visibility collapse", "visibility:collapse"},
		{"opacity zero", "opacity:0"},
		{"opacity zero float", "opacity:0.0"},
		{"opacity zero percent", "opacity:0%"},
		{"opacity zero, important", "opacity:0 !important"},
		{"font size zero", "font-size:0"},
		{"font size zero px", "font-size:0px"},
		{"text indent parked", "text-indent:-9999px"},
		{"text indent parked, percent", "text-indent:-100000%"},
		{"transform scale", "transform:scale(0)"},
		{"transform scale float", "transform:scale(0.0)"},
		{"transform scale axis", "transform:scaleX(0)"},
		{"transform scale with translate", "transform:scale(0) translate(10px)"},
		{"transform matrix zero", "transform:matrix(0,0,0,0,0,0)"},
		{"clip-path inset", "clip-path:inset(100%)"},
		{"clip-path inset, spaced", "clip-path: inset(100% 100%)"},
		{"clip-path circle zero", "clip-path:circle(0)"},
		{"clip rect zero", "clip:rect(0,0,0,0)"},
		{"clip rect zero px", "clip:rect(0px, 0px, 0px, 0px)"},
		{"zero box clipped", "width:0;height:0;overflow:hidden"},
		{"zero box clipped, reordered", "overflow:hidden;height:0px;width:0"},
		{"absolute parked", "position:absolute;left:-9999px"},
		{"fixed parked", "position:fixed;top:-99999px"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := `<p style="` + tc.style + `">SECRET</p><p>VISIBLE</p>`
			got := strip(t, in)
			if strings.Contains(got, "SECRET") {
				t.Errorf("hidden content leaked through style %q: %q", tc.style, got)
			}
			if !strings.Contains(got, "VISIBLE") {
				t.Errorf("visible sibling lost with style %q: %q", tc.style, got)
			}
		})
	}
}

// TestHiddenAttributeMatrix covers the markers that hide an element
// without a style declaration.
func TestHiddenAttributeMatrix(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"hidden attribute", `<p hidden>SECRET</p><p>VISIBLE</p>`},
		{"aria hidden", `<p aria-hidden="true">SECRET</p><p>VISIBLE</p>`},
		{"aria hidden, upper case", `<p aria-hidden="True">SECRET</p><p>VISIBLE</p>`},
		{"hidden input", `<input type="hidden" value="SECRET"><p>VISIBLE</p>`},
		{"hidden input, upper case", `<input type="HIDDEN" value="SECRET"><p>VISIBLE</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strip(t, tc.in)
			if strings.Contains(got, "SECRET") {
				t.Errorf("hidden content leaked: %q", got)
			}
			if !strings.Contains(got, "VISIBLE") {
				t.Errorf("visible content lost: %q", got)
			}
		})
	}
}

// TestHiddenStyleKeepsVisibleSpellings pins the other edge of the
// matrix: every case here is a declaration near a hiding rule without
// being one, and its content must survive.
func TestHiddenStyleKeepsVisibleSpellings(t *testing.T) {
	cases := []struct {
		name  string
		style string
	}{
		{"opacity near zero", "opacity:0.1"},
		{"display other value", "display:block"},
		{"visibility other value", "visibility:visible"},
		{"font size small", "font-size:1px"},
		{"text indent right", "text-indent:9999px"},
		{"text indent nudged", "text-indent:-99px"},
		{"transform scale up", "transform:scale(1)"},
		{"transform scale half", "transform:scale(0.5)"},
		{"transform translate", "transform:translate(0)"},
		{"transform rotate", "transform:rotate(0)"},
		{"clip-path partial inset", "clip-path:inset(50%)"},
		{"clip-path circle sized", "clip-path:circle(50%)"},
		{"clip rect sized", "clip:rect(0,10px,10px,0)"},
		{"width alone zero", "width:0"},
		{"height alone zero", "height:0"},
		{"zero box not clipped", "width:0;height:0;overflow:visible"},
		{"absolute on screen", "position:absolute;left:10px"},
		{"static parked", "position:static;left:-9999px"},
		{"unparsed style", "just some words"},
		{"empty style", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := `<p style="` + tc.style + `">SECRET</p>`
			if got := strip(t, in); !strings.Contains(got, "SECRET") {
				t.Errorf("visible content dropped by style %q: %q", tc.style, got)
			}
		})
	}
}

func strip(t *testing.T, in string) string {
	t.Helper()
	out, err := StripHiddenHTML(strings.NewReader(in), DefaultSanitizeConfig)
	if err != nil {
		t.Fatalf("StripHiddenHTML: %v", err)
	}
	return out
}
