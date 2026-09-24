package core

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	goNames := []string{
		EventReady, EventFatal, EventStatus,
		EventUsage, EventStream, EventArtifact, EventInteract,
		EventResolved, EventSteerPending, EventTurnEnd,
		EventSessionUpdated, EventAutomationChanged, EventAutomationRun,
		EventAutomationRunStarted, EventManagedRestored,
		EventGitChanged, EventInferenceChanged, EventTelemetryChanged,
		EventPetPacksChanged, EventPetSettingsChanged, EventPetRuntimeStatus,
	}
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
	// Only "key: 'value'," lines: comments in the block contain
	// apostrophes too.
	var tsNames []string
	valueRe := regexp.MustCompile(`(?m)^\s*[A-Za-z][A-Za-z0-9]*:\s*'([^']+)',?\s*$`)
	for _, m := range valueRe.FindAllStringSubmatch(block[1], -1) {
		tsNames = append(tsNames, m[1])
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
