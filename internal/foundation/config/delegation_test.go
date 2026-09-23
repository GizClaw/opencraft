package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDelegationResolveDefaultsAndBounds pins the two service limits
// and the target lists: a zero limit means the shipped default, the
// ceilings bound what one workspace may configure, and the lists are
// trimmed, de-duped and checked for patterns a matcher could never use.
func TestDelegationResolveDefaultsAndBounds(t *testing.T) {
	defaults := DefaultDelegationSettings()
	if defaults.MaxConcurrency != 4 || defaults.MaxDepth != 8 {
		t.Fatalf("shipped defaults = %+v", defaults)
	}
	if len(defaults.AllowedTargets) != 0 || len(defaults.BlockedTargets) != 0 {
		t.Fatalf("shipped defaults restrict: %+v", defaults)
	}

	resolved, err := DelegationSettings{}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.MaxConcurrency != 4 || resolved.MaxDepth != 8 {
		t.Fatalf("zero limits = %+v, want the defaults", resolved)
	}

	// The ceilings themselves are accepted; a value past them is not.
	if _, err := (DelegationSettings{
		MaxConcurrency: DelegationMaxConcurrencyCeiling,
		MaxDepth:       DelegationMaxDepthCeiling,
	}).Resolve(); err != nil {
		t.Fatalf("the ceilings must be accepted: %v", err)
	}
	for _, bad := range []DelegationSettings{
		{MaxConcurrency: -1},
		{MaxConcurrency: DelegationMaxConcurrencyCeiling + 1},
		{MaxDepth: -1},
		{MaxDepth: DelegationMaxDepthCeiling + 1},
	} {
		if _, err := bad.Resolve(); err == nil {
			t.Fatalf("Resolve(%+v) accepted an out-of-range limit", bad)
		}
	}

	// Lists: blanks dropped, whitespace trimmed, duplicates collapsed.
	normalized, err := DelegationSettings{
		AllowedTargets: []string{" researcher ", "", "researcher", "writer*"},
		BlockedTargets: []string{"danger*"},
	}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(normalized.AllowedTargets, ","); got != "researcher,writer*" {
		t.Fatalf("allowed = %q, want the trimmed de-duped list", got)
	}
	if got := strings.Join(normalized.BlockedTargets, ","); got != "danger*" {
		t.Fatalf("blocked = %q", got)
	}

	for name, bad := range map[string]DelegationSettings{
		"whitespace in a name": {AllowedTargets: []string{"two words"}},
		"a path in a name":     {AllowedTargets: []string{"a/b"}},
		"a malformed pattern":  {AllowedTargets: []string{"[unclosed"}},
		"a name on both lists": {
			AllowedTargets: []string{"researcher"},
			BlockedTargets: []string{"researcher"},
		},
	} {
		if _, err := bad.Resolve(); err == nil {
			t.Errorf("Resolve accepted %s", name)
		}
	}

	over := make([]string, MaxDelegationTargets+1)
	for i := range over {
		over[i] = fmt.Sprintf("target-%d", i)
	}
	if _, err := (DelegationSettings{AllowedTargets: over}).Resolve(); err == nil {
		t.Fatal("Resolve accepted a list over the bound")
	}
	// The runtime normalizer truncates instead of dropping the list: an
	// emptied allowlist would mean "no restriction at all".
	if got := NormalizeTargetPatterns(over); len(got) != MaxDelegationTargets {
		t.Fatalf("NormalizeTargetPatterns over the bound = %d entries, want %d",
			len(got), MaxDelegationTargets)
	}
}

// TestDelegationUserLayerRoundTrip pins Load -> Save -> Load, that
// clearing the lists works, that a hand-written sibling key inside the
// delegate resource survives a save, and that the policy resource is
// replaced wholesale so a removed target cannot linger.
func TestDelegationUserLayerRoundTrip(t *testing.T) {
	dir := t.TempDir()

	embedded, err := LoadDelegation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.MaxConcurrency != 4 || embedded.MaxDepth != 8 ||
		len(embedded.AllowedTargets) != 0 || len(embedded.BlockedTargets) != 0 {
		t.Fatalf("embedded defaults = %+v", embedded)
	}

	saved := DelegationSettings{
		MaxConcurrency: 8,
		MaxDepth:       4,
		AllowedTargets: []string{"researcher", "writer*"},
		BlockedTargets: []string{"danger*"},
	}
	if err := SaveDelegation(dir, saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDelegation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MaxConcurrency != 8 || loaded.MaxDepth != 4 {
		t.Fatalf("loaded limits = %+v", loaded)
	}
	if got := strings.Join(loaded.AllowedTargets, ","); got != "researcher,writer*" {
		t.Fatalf("loaded allowed = %q", got)
	}
	if got := strings.Join(loaded.BlockedTargets, ","); got != "danger*" {
		t.Fatalf("loaded blocked = %q", got)
	}

	// Saving empty lists clears the restriction: the policy resource is
	// replaced, and the embedded layer underneath carries none.
	if err := SaveDelegation(dir, DelegationSettings{
		MaxConcurrency: 8, MaxDepth: 4,
	}); err != nil {
		t.Fatal(err)
	}
	cleared, err := LoadDelegation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.AllowedTargets) != 0 || len(cleared.BlockedTargets) != 0 {
		t.Fatalf("cleared lists = %+v", cleared)
	}

	// A user layer that also carries a hand-written delegate key: the
	// generated settings are deep-merged into it, and the policy
	// resource is generator-owned, so its old list disappears.
	handWritten := strings.Join([]string{
		"version: v1",
		"resources:",
		"  delegate:",
		"    kind: delegation.Service",
		"    impl: local",
		"    deps:",
		"      backend: delegate.backend",
		"    settings:",
		"      hand_written: true",
		"  delegate.policy:",
		"    kind: opencraft.delegation.policy",
		"    impl: local",
		"    settings:",
		"      allowed_targets:",
		"        - stale-target",
		"",
	}, "\n")
	path := filepath.Join(dir, "opencraft.yaml")
	if err := os.WriteFile(path, []byte(handWritten), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveDelegation(dir, DelegationSettings{
		MaxConcurrency: 2,
		MaxDepth:       2,
		AllowedTargets: []string{"researcher"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "hand_written: true") ||
		!strings.Contains(text, "backend: delegate.backend") {
		t.Fatalf("a hand-written key was lost:\n%s", text)
	}
	if strings.Contains(text, "stale-target") {
		t.Fatalf("a removed target lingered:\n%s", text)
	}
	reloaded, err := LoadDelegation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.MaxConcurrency != 2 || reloaded.MaxDepth != 2 ||
		strings.Join(reloaded.AllowedTargets, ",") != "researcher" {
		t.Fatalf("reloaded = %+v", reloaded)
	}

	// A refused save leaves the stored layer alone.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveDelegation(dir, DelegationSettings{
		MaxConcurrency: DelegationMaxConcurrencyCeiling + 1,
	}); err == nil {
		t.Fatal("SaveDelegation must refuse out-of-range settings")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused save rewrote the layer")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("user layer mode = %o, want 0600", perm)
	}
}
