package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEventNamesMatchFrontendMap keeps the two halves of the UI event
// contract equal: this package's constants, which the emit sites use,
// and frontend/src/lib/events.ts, which every listener uses. A name that
// exists on one side only is either an event the frontend can never
// receive or a listener that can never fire, and both fail silently.
func TestEventNamesMatchFrontendMap(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "frontend", "src", "lib", "events.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("frontend event map not available: %v", err)
	}
	ts := string(data)

	goNames := eventConstantValues(t)
	// A duplicate means two constants spelling the same wire name: one of
	// them is a copy-paste mistake that would be invisible at the call
	// site.
	seen := make(map[string]bool, len(goNames))
	for _, name := range goNames {
		if seen[name] {
			t.Fatalf("duplicate Go event name %q", name)
		}
		seen[name] = true
	}

	block := regexp.MustCompile(`(?s)export const UIEventType = \{(.*?)\} as const;`).
		FindStringSubmatch(ts)
	if block == nil {
		t.Fatalf("UIEventType block not found in %s", path)
	}
	// Every key in the block is counted, and only then are values read
	// off the lines that parse. A value this reader cannot see (double
	// quotes, a trailing comment) would otherwise drop a name from the
	// comparison below, which is the one thing the comparison exists to
	// catch.
	keyRe := regexp.MustCompile(`(?m)^\s*[A-Za-z][A-Za-z0-9]*\s*:`)
	keys := keyRe.FindAllString(block[1], -1)
	var tsNames []string
	valueRe := regexp.MustCompile(`(?m)^\s*[A-Za-z][A-Za-z0-9]*\s*:\s*(['"][^'"]+['"])\s*,?\s*$`)
	for _, m := range valueRe.FindAllStringSubmatch(block[1], -1) {
		tsNames = append(tsNames, strings.Trim(m[1], `'"`))
	}
	if len(tsNames) != len(keys) {
		t.Fatalf("read %d of the %d UIEventType entries in %s: a value "+
			"this test cannot read would hide a name only one side has",
			len(tsNames), len(keys), path)
	}
	compareNameSets(t, goNames, tsNames)

	for _, tc := range []struct{ name, want string }{
		{"UIEventChannel", UIEventChannel},
		{"MenuCommandChannel", MenuCommandEvent},
		{"PetStateChannel", EventPetState},
	} {
		re := regexp.MustCompile(`export const ` + tc.name + ` = '([^']*)'`)
		m := re.FindStringSubmatch(ts)
		if m == nil {
			t.Errorf("%s not found in %s", tc.name, path)
			continue
		}
		if m[1] != tc.want {
			t.Errorf("%s = %q in frontend, want %q", tc.name, m[1], tc.want)
		}
	}
}

// eventConstantValues reads event_names.go and returns the value of
// every event constant it declares. Scanning the file is what keeps a
// new event type from slipping through: a hand-copied list here passes
// when a constant is added and forgotten, which is exactly the drift
// the comparison cannot see. EventPetState is skipped because it names
// the pet window's own channel, not a type on UIEventChannel; its value
// is compared with the other channel constants instead.
func eventConstantValues(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "event_names.go", nil, 0)
	if err != nil {
		t.Fatalf("parse event_names.go: %v", err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range vs.Names {
				if !strings.HasPrefix(ident.Name, "Event") ||
					ident.Name == "EventPetState" {
					continue
				}
				if i >= len(vs.Values) {
					t.Fatalf("%s: constant has no value to read", ident.Name)
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: value is not a string literal", ident.Name)
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", ident.Name, err)
				}
				names = append(names, value)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("event_names.go declares no event constants")
	}
	return names
}

func compareNameSets(t *testing.T, goNames, tsNames []string) {
	t.Helper()
	sorted := func(names []string) []string {
		out := append([]string(nil), names...)
		sort.Strings(out)
		return out
	}
	goSet := sorted(goNames)
	tsSet := sorted(tsNames)
	if strings.Join(goSet, ",") == strings.Join(tsSet, ",") {
		return
	}
	inGo := make(map[string]bool, len(goNames))
	for _, name := range goNames {
		inGo[name] = true
	}
	inTS := make(map[string]bool, len(tsNames))
	for _, name := range tsNames {
		inTS[name] = true
	}
	for _, name := range goSet {
		if !inTS[name] {
			t.Errorf("Go event %q is missing from the frontend map", name)
		}
	}
	for _, name := range tsSet {
		if !inGo[name] {
			t.Errorf("frontend event %q is missing from the Go registry", name)
		}
	}
}
