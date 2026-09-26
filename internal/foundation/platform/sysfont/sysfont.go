// Package sysfont lists the font families installed on the host.
//
// It is the only place that knows how each desktop OS exposes its font
// catalogue: CoreText on macOS, GDI on Windows and fontconfig on Linux.
// Callers treat an empty list as "this platform cannot enumerate fonts" and
// fall back to a typed family name, so a missing or restricted catalogue
// degrades the picker instead of breaking the settings page.
//
// The catalogue is cached briefly: the settings page asks for it whenever it
// opens, while the underlying queries walk the system font store.
package sysfont

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// cacheTTL keeps a freshly installed font from being hidden for the whole
// process lifetime without re-reading the catalogue on every settings visit.
const cacheTTL = 10 * time.Minute

var (
	cacheMu   sync.Mutex
	cacheList []string
	cacheErr  error
	cacheAt   time.Time
)

// List returns the installed font families, sorted case-insensitively and
// deduplicated. The returned slice is a copy the caller may keep.
func List() ([]string, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if time.Since(cacheAt) < cacheTTL {
		return append([]string(nil), cacheList...), cacheErr
	}
	raw, err := listSystemFonts()
	if err != nil {
		// Keep the previous catalogue: a transient failure (a wedged
		// fontconfig cache, a busy font registry) must not empty the
		// picker the user is looking at.
		cacheErr = err
		cacheAt = time.Now()
		return append([]string(nil), cacheList...), err
	}
	cacheList = filterFamilies(raw)
	cacheErr = nil
	cacheAt = time.Now()
	return append([]string(nil), cacheList...), nil
}

// filterFamilies normalizes a platform catalogue: trims, drops the private
// faces whose names start with a dot (macOS system fonts that are not
// user-selectable) or an at sign (the vertical-writing variants GDI adds on
// Windows), drops the icon-only faces below, deduplicates case-insensitively
// and sorts.
func filterFamilies(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		name = strings.TrimSpace(name)
		if name == "" ||
			strings.HasPrefix(name, ".") ||
			strings.HasPrefix(name, "@") ||
			iconOnlyFamily(name) {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	sortFamilies(out)
	return out
}

// iconOnlyFamilyPrefixes lists families that draw icon or emoji artwork
// rather than text. Picking one changes nothing visible — the interface text
// falls through to the next family in the stack — which reads as "the setting
// did nothing", so the picker leaves them out.
//
// The match is a case-insensitive prefix so versioned cuts are covered
// ("Font Awesome 6 Free", "Symbols Nerd Font Mono"), and the list stays
// deliberately narrow: coding fonts that merely carry Nerd Font glyphs
// ("0xProto Nerd Font", "Maple Mono NF CN") and system text faces with
// similar names ("Segoe UI Variable") must keep showing up.
var iconOnlyFamilyPrefixes = []string{
	"apple color emoji",
	"apple symbols",
	"bootstrap icons",
	"emoji",
	"font awesome",
	"fonticons",
	"glyphicons",
	"google symbols",
	"icomoon",
	"material design icons",
	"material icons",
	"material symbols",
	"noto color emoji",
	"noto emoji",
	"octicons",
	"powerline",
	"segoe fluent icons",
	"segoe mdl2 assets",
	"segoe ui emoji",
	"segoe ui symbol",
	"symbols nerd font",
	"themify",
	"twemoji",
	"typicons",
	"weather icons",
}

// iconOnlyFamily reports whether a family name is one of the icon-only faces.
func iconOnlyFamily(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range iconOnlyFamilyPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// sortFamilies orders families the way a picker should list them: grouped by
// letter, with a stable tie-break so equal keys keep a deterministic order.
func sortFamilies(families []string) {
	sort.SliceStable(families, func(i, j int) bool {
		left, right := strings.ToLower(families[i]), strings.ToLower(families[j])
		if left == right {
			return families[i] < families[j]
		}
		return left < right
	})
}
