package ids

import (
	"strings"
	"testing"
)

// TestPrefixesAreWireValues pins the three prefixes to the literals
// other programs already spell. The values cross a process boundary —
// core mints `run-` and `ctx-` ids, `s-` names session directories
// every shipped build wrote — so changing one is a migration, and this
// test is the place that says so.
func TestPrefixesAreWireValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
		why  string
	}{
		{
			name: "SessionPrefix", got: SessionPrefix, want: "s-",
			why: "session directories on disk and existing conversation ids are named s-*",
		},
		{
			name: "ContextPrefix", got: ContextPrefix, want: "ctx-",
			why: "flowcraft core mints delegated and review contexts as ctx-*",
		},
		{
			name: "RunPrefix", got: RunPrefix, want: "run-",
			why: "core's graph run ids carry it and checkpoint rows are keyed by them",
		},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q: %s", tc.name, tc.got, tc.want, tc.why)
		}
	}
}

func TestNewSessionShape(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 64; i++ {
		id := NewSession()
		if !IsSession(id) {
			t.Fatalf("NewSession produced %q, which IsSession rejects", id)
		}
		if len(id) != len(SessionPrefix)+16 {
			t.Fatalf("NewSession produced %q, want %d hex characters after the prefix",
				id, 16)
		}
		if seen[id] {
			t.Fatalf("NewSession repeated %q", id)
		}
		seen[id] = true
	}
}

func TestIsSessionShape(t *testing.T) {
	valid := []string{"s-1", "s-", "s-default", "s-" + strings.Repeat("f", 16)}
	for _, id := range valid {
		if !IsSession(id) {
			t.Errorf("IsSession(%q) = false, want true", id)
		}
	}
	invalid := []string{
		"", "s", "ss-1", "-s-1", "S-1",
		"ctx-1", "run-1", "session",
		"s-a/b", `s-a\b`, "../s-1", "s-1/..",
	}
	for _, id := range invalid {
		if IsSession(id) {
			t.Errorf("IsSession(%q) = true, want false", id)
		}
	}
}

func TestIsContextAndRun(t *testing.T) {
	if !IsContext("ctx-4abfc8f0") || IsContext("s-1") || IsContext("") {
		t.Error("IsContext does not classify ctx- ids")
	}
	if !IsRun("run-1") || IsRun("ctx-1") || IsRun("session-s-1") || IsRun("") {
		t.Error("IsRun does not classify run- ids")
	}
}
