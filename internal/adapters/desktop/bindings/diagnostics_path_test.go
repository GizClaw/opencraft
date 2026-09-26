package bindings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/platform/envpath"
)

// TestPathEnvironmentWireShapeUsesLists is the regression test for the
// settings page crashing on the diagnostics tab: an empty Go slice
// marshals to null, and the renderer read the field as a list.
func TestPathEnvironmentWireShapeUsesLists(t *testing.T) {
	c := core.NewCore(t.TempDir(), t.TempDir(), "")
	c.SetPathReport(envpath.Result{Plan: envpath.Plan{Path: "/usr/bin"}})

	raw, err := json.Marshal(NewDiagnosticsBinding(c).PathEnvironment())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"segments":[]`, `"prepend":[]`, `"rejected":[]`, `"missing":[]`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("payload %s does not carry %s", raw, want)
		}
	}
}

func TestPathEnvironmentReportsProvenance(t *testing.T) {
	inherited := t.TempDir()
	prepended := t.TempDir()
	candidate := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-installed")
	t.Setenv("PATH", inherited)

	c := core.NewCore(t.TempDir(), t.TempDir(), "")
	if err := c.Shell.SetPathPrepend([]string{prepended}); err != nil {
		t.Fatalf("SetPathPrepend: %v", err)
	}
	resolved, err := envpath.Install(envpath.Options{
		Prepend:    []string{prepended},
		Candidates: []string{candidate, missing},
	})
	if err != nil {
		t.Fatalf("envpath.Install: %v", err)
	}
	c.SetPathReport(resolved)

	dto := NewDiagnosticsBinding(c).PathEnvironment()
	if dto.Path != strings.Join([]string{prepended, inherited, candidate},
		string(filepath.ListSeparator)) {
		t.Fatalf("path = %q, want prepend, inherited, candidate", dto.Path)
	}
	want := map[string]string{
		prepended: envpath.SourcePrepend,
		inherited: envpath.SourceInherited,
		candidate: envpath.SourceCandidate,
	}
	if len(dto.Segments) != len(want) {
		t.Fatalf("segments = %+v, want %d entries", dto.Segments, len(want))
	}
	for _, segment := range dto.Segments {
		if segment.Source != want[segment.Dir] {
			t.Fatalf("segment %+v, want source %q", segment, want[segment.Dir])
		}
		if !segment.Present {
			t.Fatalf("segment %+v reports absent, want present", segment)
		}
	}
	if len(dto.Prepend) != 1 || dto.Prepend[0] != prepended {
		t.Fatalf("prepend = %v, want [%s]", dto.Prepend, prepended)
	}
	if len(dto.Missing) != 1 || dto.Missing[0] != missing {
		t.Fatalf("missing = %v, want [%s]", dto.Missing, missing)
	}
}

func TestPathEnvironmentRejectsRelativeOverride(t *testing.T) {
	c := core.NewCore(t.TempDir(), t.TempDir(), "")
	binding := NewDiagnosticsBinding(c)
	if _, err := binding.SetPathPrepend([]string{"relative/bin"}); err == nil {
		t.Fatal("SetPathPrepend(relative) succeeded, want an error")
	}
	// The rejected value was not persisted, so the stored override stays
	// empty and nothing was applied.
	if got := c.Shell.PathPrepend(); len(got) != 0 {
		t.Fatalf("persisted prepend = %v, want empty", got)
	}
}

// TestPathEnvironmentReportsHandEditedRelativeEntry covers the document a
// user edited by hand: the value loads, and the diagnostics view is where
// it shows up as rejected instead of silently disappearing.
func TestPathEnvironmentReportsHandEditedRelativeEntry(t *testing.T) {
	userDir := t.TempDir()
	document := `{"path":{"prepend":["relative/bin","/opt/definitely-missing"]}}`
	if err := os.WriteFile(
		filepath.Join(userDir, "desktop.json"), []byte(document), 0o600,
	); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	c := core.NewCore(userDir, t.TempDir(), "")

	dto := NewDiagnosticsBinding(c).PathEnvironment()
	if len(dto.Rejected) != 1 || dto.Rejected[0] != "relative/bin" {
		t.Fatalf("rejected = %v, want [relative/bin]", dto.Rejected)
	}
	// The override is kept on the document so the editor can show the
	// entry the user typed, and the absolute entry that does not exist
	// yet stays on PATH marked absent.
	if len(dto.Prepend) != 2 {
		t.Fatalf("prepend = %v, want both stored entries", dto.Prepend)
	}
	for _, segment := range dto.Segments {
		if segment.Dir == "/opt/definitely-missing" && segment.Present {
			t.Fatal("missing prepend entry reported as present")
		}
	}
}

func TestSetPathPrependAppliesToProcessPath(t *testing.T) {
	inherited := t.TempDir()
	added := t.TempDir()
	t.Setenv("PATH", inherited)
	c := core.NewCore(t.TempDir(), t.TempDir(), "")

	dto, err := NewDiagnosticsBinding(c).SetPathPrepend([]string{added})
	if err != nil {
		t.Fatalf("SetPathPrepend: %v", err)
	}
	if !strings.HasPrefix(dto.Path, added) {
		t.Fatalf("path = %q, want it to start with %s", dto.Path, added)
	}
	if got := os.Getenv("PATH"); got != dto.Path {
		t.Fatalf("process PATH = %q, want %q", got, dto.Path)
	}
	if !dto.Reloaded {
		t.Fatal("SetPathPrepend reported no reload with no workspace open")
	}
}
