package telemetry

import (
	"encoding/json"
	"testing"
)

// Header names travel to the diagnostics card as JSON, where a nil Go
// slice marshals to null and the card read .length straight off it. The
// frontend half of the finding guards that read; this half stops
// producing the null. The struct mirrors the wire shape of
// core.PluginTelemetryState.
func TestHeaderNamesMarshalAsAList(t *testing.T) {
	wire := func(names []string) string {
		t.Helper()
		raw, err := json.Marshal(struct {
			HeaderNames []string `json:"headerNames"`
		}{HeaderNames: names})
		if err != nil {
			t.Fatalf("marshal header names: %v", err)
		}
		return string(raw)
	}

	if names := (Sink{}).HeaderNames(); names == nil || len(names) != 0 {
		t.Fatalf("empty sink names = %#v, want an empty non-nil slice", names)
	}
	if got := wire((Sink{}).HeaderNames()); got != `{"headerNames":[]}` {
		t.Fatalf("empty sink on the wire = %s, want {\"headerNames\":[]}", got)
	}

	named := Sink{Headers: map[string]string{"Zeta": "z", "alpha": "a"}}
	names := named.HeaderNames()
	if len(names) != 2 || names[0] != "Zeta" || names[1] != "alpha" {
		t.Fatalf("names = %v, want the sorted [Zeta alpha]", names)
	}
	if got := wire(names); got != `{"headerNames":["Zeta","alpha"]}` {
		t.Fatalf("named sink on the wire = %s", got)
	}
}
