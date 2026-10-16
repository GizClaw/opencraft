package extract

import (
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SanitizeConfig defines which sanitization rules to apply for
// StripHiddenHTML.
type SanitizeConfig struct {
	RemoveHiddenElements bool
	RemoveScripts        bool
	RemoveStyles         bool
}

// DefaultSanitizeConfig is the standard config for hidden element
// stripping.
var DefaultSanitizeConfig = SanitizeConfig{
	RemoveHiddenElements: true,
	RemoveScripts:        true,
	RemoveStyles:         true,
}

// nonTextTags are tags that never contain visible text.
var nonTextTags = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Template: true,
	atom.Svg:      true,
	atom.Canvas:   true,
	atom.Iframe:   true,
	atom.Object:   true,
	atom.Embed:    true,
}

// StripHiddenHTML removes hidden elements and non-text tags from HTML.
func StripHiddenHTML(r io.Reader, cfg SanitizeConfig) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}

	var toRemove []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if cfg.RemoveHiddenElements && isHiddenElement(n) {
				toRemove = append(toRemove, n)
				return
			}
			if cfg.RemoveScripts && n.DataAtom == atom.Script {
				toRemove = append(toRemove, n)
				return
			}
			if cfg.RemoveStyles && n.DataAtom == atom.Style {
				toRemove = append(toRemove, n)
				return
			}
			if nonTextTags[n.DataAtom] {
				toRemove = append(toRemove, n)
				return
			}
		}
		if n.Type == html.CommentNode {
			toRemove = append(toRemove, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	for _, node := range toRemove {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
	}

	var sb strings.Builder
	if err := html.Render(&sb, doc); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// isHiddenElement reports whether an element is hidden by an attribute:
// the hidden and aria-hidden attributes, a hidden input, or a style
// declaration that hides its content (see matchesHiddenStyle).
func isHiddenElement(n *html.Node) bool {
	for _, attr := range n.Attr {
		if attr.Key == "hidden" {
			return true
		}
		if attr.Key == "aria-hidden" && strings.EqualFold(attr.Val, "true") {
			return true
		}
		if attr.Key == "type" && n.DataAtom == atom.Input &&
			strings.EqualFold(attr.Val, "hidden") {
			return true
		}
		if attr.Key == "style" {
			if matchesHiddenStyle(attr.Val) {
				return true
			}
		}
	}
	return false
}

// matchesHiddenStyle reports whether a style attribute hides the
// element's content.
//
// The declarations are parsed before they are judged, and their numbers
// are compared as numbers, because the same invisibility has many
// spellings: opacity:0.0, opacity:0%, transform:scale(0.00), and
// clip:rect(0px,0px,0px,0px) all hid before, as did a style with
// interior whitespace such as "display : none". Substring matching over
// the raw attribute saw none of them. A rule this list does not know
// about still stays; so does anything hidden by a stylesheet.
func matchesHiddenStyle(style string) bool {
	decls := parseStyleDeclarations(style)
	if len(decls) == 0 {
		return false
	}
	var zeroWidth, zeroHeight, clippedOverflow bool
	var positioned, parkedOffscreen bool
	for _, d := range decls {
		switch d.name {
		case "display":
			if d.value == "none" {
				return true
			}
		case "visibility":
			// "collapse" hides table rows and columns.
			if d.value == "hidden" || d.value == "collapse" {
				return true
			}
		case "opacity", "font-size":
			// font-size:0 leaves the text zero-sized, which is how a
			// lot of scraped pages park keywords out of sight.
			if isZeroValue(d.value) {
				return true
			}
		case "text-indent":
			if isParkedOffscreen(d.value) {
				return true
			}
		case "transform":
			if collapsesToNothing(d.value) {
				return true
			}
		case "clip-path":
			if isDegenerateClip(d.value) {
				return true
			}
		case "clip":
			if isZeroRect(d.value) {
				return true
			}
		case "width":
			zeroWidth = isZeroValue(d.value)
		case "height":
			zeroHeight = isZeroValue(d.value)
		case "overflow":
			if d.value == "hidden" {
				clippedOverflow = true
			}
		case "position":
			positioned = d.value == "absolute" || d.value == "fixed"
		case "left", "top", "right", "bottom":
			if isParkedOffscreen(d.value) {
				parkedOffscreen = true
			}
		}
	}
	// A zero-sized box that clips whatever it holds shows nothing.
	if zeroWidth && zeroHeight && clippedOverflow {
		return true
	}
	// The other classic: take the element out of the flow and park it
	// past the edge of the viewport.
	return positioned && parkedOffscreen
}

// styleDecl is one normalized declaration of a style attribute.
type styleDecl struct {
	name  string
	value string
}

// parseStyleDeclarations splits a style attribute into declarations
// whose equivalent spellings compare equal: names and values are
// lower-cased, whitespace is dropped, and a trailing !important is
// removed from the value.
func parseStyleDeclarations(style string) []styleDecl {
	var decls []styleDecl
	for _, raw := range strings.Split(style, ";") {
		name, value, ok := strings.Cut(raw, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = normalizeStyleValue(value)
		if name == "" || value == "" {
			continue
		}
		decls = append(decls, styleDecl{name: name, value: value})
	}
	return decls
}

func normalizeStyleValue(value string) string {
	// Fields collapses every run of whitespace, so "  none !important "
	// becomes "none!important" and the marker can be trimmed.
	value = strings.Join(strings.Fields(strings.ToLower(value)), "")
	return strings.TrimSuffix(value, "!important")
}

// styleNumberRe matches one CSS number: 0, 0.5, .5, -9999.
var styleNumberRe = regexp.MustCompile(`-?(?:\d+(?:\.\d+)?|\.\d+)`)

// styleFunctionRe matches one function call inside a declaration value.
var styleFunctionRe = regexp.MustCompile(`([a-z0-9-]+)\(([^()]*)\)`)

// zeroValueRe matches a value that is a single zero, written any way
// CSS allows: 0, 0.0, .0, 0px, 0%.
var zeroValueRe = regexp.MustCompile(`^-?(?:0+(?:\.0+)?|\.0+)(?:[a-z]+|%)?$`)

// offscreenRe matches a single negative length whose magnitude is at
// least 9999 units.
var offscreenRe = regexp.MustCompile(`^-(\d+(?:\.\d+)?|\.\d+)(?:[a-z]+|%)?$`)

func isZeroValue(value string) bool { return zeroValueRe.MatchString(value) }

// isParkedOffscreen reports whether a value is a negative length that
// puts the element far past the top or left edge of the viewport.
func isParkedOffscreen(value string) bool {
	m := offscreenRe.FindStringSubmatch(value)
	if m == nil {
		return false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	return err == nil && n >= 9999
}

// styleNumbers returns every number a value contains. A malformed
// number is an error: the caller then leaves the declaration alone
// instead of guessing what it says.
func styleNumbers(value string) ([]float64, error) {
	raw := styleNumberRe.FindAllString(value, -1)
	nums := make([]float64, 0, len(raw))
	for _, r := range raw {
		n, err := strconv.ParseFloat(r, 64)
		if err != nil {
			return nil, err
		}
		nums = append(nums, n)
	}
	return nums, nil
}

// collapsesToNothing reports whether a transform scales the element
// away: scale(0), scale(0,0), scale3d(0,0,0), matrix(0,...). Translate
// and rotate move content around without erasing it, so they are not
// this case.
func collapsesToNothing(value string) bool {
	for _, call := range styleFunctionRe.FindAllStringSubmatch(value, -1) {
		name, args := call[1], call[2]
		if !strings.HasPrefix(name, "scale") && name != "matrix" {
			continue
		}
		nums, err := styleNumbers(args)
		if err != nil || len(nums) == 0 {
			continue
		}
		all := true
		for _, n := range nums {
			if n != 0 {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// isDegenerateClip reports whether a clip-path leaves nothing visible:
// inset(100%) (or any mix of sides that reaches 100%), or a circle or
// ellipse whose radius is zero.
func isDegenerateClip(value string) bool {
	for _, call := range styleFunctionRe.FindAllStringSubmatch(value, -1) {
		name, args := call[1], call[2]
		nums, err := styleNumbers(args)
		if err != nil || len(nums) == 0 {
			continue
		}
		switch name {
		case "inset":
			all := true
			for _, n := range nums {
				if n < 100 {
					all = false
					break
				}
			}
			if all {
				return true
			}
		case "circle", "ellipse":
			all := true
			for _, n := range nums {
				if n != 0 {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
	}
	return false
}

// isZeroRect reports whether the legacy clip property clips to an empty
// rectangle: rect(0,0,0,0), however its zeros are written.
func isZeroRect(value string) bool {
	call := styleFunctionRe.FindStringSubmatch(value)
	if call == nil || call[1] != "rect" {
		return false
	}
	nums, err := styleNumbers(call[2])
	if err != nil || len(nums) == 0 {
		return false
	}
	for _, n := range nums {
		if n != 0 {
			return false
		}
	}
	return true
}

// getAttr gets an attribute value by name.
func getAttr(n *html.Node, name string) string {
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}
