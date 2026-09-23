package desktop

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/procmem"
)

func TestFamilySamplesSplitByRole(t *testing.T) {
	family := procmem.Family{
		Root:   100,
		Source: procmem.SourceResponsibility,
		Members: []procmem.Process{
			{PID: 100, Name: "opencraft", Role: procmem.RoleSelf, Footprint: 100},
			{PID: 101, Name: "opencraft", Role: procmem.RoleChild, Footprint: 20},
			{PID: 102, Name: "node", Role: procmem.RoleChild, Footprint: 30},
			{PID: 200, Name: "com.apple.WebKit.WebContent", Role: procmem.RoleWebContent, Footprint: 400},
		},
	}
	samples := familySamples(7, family)
	if len(samples) != 5 {
		t.Fatalf("samples = %d, want one per member plus the total", len(samples))
	}
	for _, sample := range samples[:4] {
		if sample.Name != procMetricFootprint {
			t.Errorf("sample %q, want %q", sample.Name, procMetricFootprint)
		}
		if sample.Ts != 7 {
			t.Errorf("ts = %d, want the sampler's tick", sample.Ts)
		}
		if sample.Attrs["role"] == "" || sample.Attrs["name"] == "" || sample.Attrs["pid"] == "" {
			t.Errorf("sample lost its role/name/pid attributes: %+v", sample.Attrs)
		}
	}
	if got := samples[1].Attrs["pid"]; got != "101" {
		t.Errorf("pid attr = %q, want 101", got)
	}
	if got := samples[3].Attrs["role"]; got != string(procmem.RoleWebContent) {
		t.Errorf("role attr = %q, want %q", got, procmem.RoleWebContent)
	}
	total := samples[4]
	if total.Name != procMetricFamilyTotal {
		t.Fatalf("last sample %q, want the family total", total.Name)
	}
	if total.Value != 550 {
		t.Errorf("total = %v, want the summed footprints", total.Value)
	}
	if got := total.Attrs["processes"]; got != "4" {
		t.Errorf("processes attr = %q, want 4", got)
	}
	if got := total.Attrs["source"]; got != procmem.SourceResponsibility {
		t.Errorf("source attr = %q, want %q", got, procmem.SourceResponsibility)
	}
}
