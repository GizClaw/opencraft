package assembly

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/tool/tooltest"
)

// repeatAssembly builds one assembly carrying only the repeat guard.
func repeatAssembly(t *testing.T, repeat string) (*tool.Assembly, *tooltest.RecordingTool) {
	t.Helper()
	rec := tooltest.NewRecordingTool("probe")
	rec.SetResponse("ok", nil)
	return newAssembly(t, `{"middlewares":{"repeat":`+repeat+`}}`, stubSource{rec}), rec
}

// repeatCtx attaches one run's tool session, which is what scopes the
// guard's counters.
func repeatCtx(asm *tool.Assembly) context.Context {
	return tool.WithSession(context.Background(), asm.NewSession())
}

func probeCall(args string) message.ToolCall {
	return message.ToolCall{
		ID:        "call-1",
		Name:      "probe",
		Arguments: json.RawMessage(args),
	}
}

// TestRepeatGuardBlocksTheThirdIdenticalCall pins the default shape:
// the first two identical calls run, the third is refused with an
// actionable error, and a retry of the refused call stays refused.
func TestRepeatGuardBlocksTheThirdIdenticalCall(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":3}`)
	ctx := repeatCtx(asm)
	call := probeCall(`{"path":"a.txt"}`)
	for i := 1; i <= 2; i++ {
		if res := asm.Execute(ctx, call); res.IsError {
			t.Fatalf("call %d blocked early: %s", i, res.Content.Text())
		}
	}

	res := asm.Execute(ctx, call)
	if !res.IsError {
		t.Fatal("third identical call was executed")
	}
	text := res.Content.Text()
	for _, want := range []string{"probe", "3 times", "in a row"} {
		if !strings.Contains(text, want) {
			t.Fatalf("blocked result %q missing %q", text, want)
		}
	}
	if got := len(rec.Calls()); got != 2 {
		t.Fatalf("tool executed %d times, want 2", got)
	}

	// Refused attempts count as the tail: retrying unchanged keeps
	// hitting the guard instead of resetting its own streak.
	if res := asm.Execute(ctx, call); !res.IsError {
		t.Fatal("retry of a refused call was executed")
	}
	if got := len(rec.Calls()); got != 2 {
		t.Fatalf("tool executed %d times after retry, want 2", got)
	}
}

// TestRepeatGuardDefaults pins the built-in number (threshold 3) for a
// deployment that enables the guard bare.
func TestRepeatGuardDefaults(t *testing.T) {
	asm, rec := repeatAssembly(t, `{"enabled":true}`)
	ctx := repeatCtx(asm)
	call := probeCall(`{"path":"a.txt"}`)
	asm.Execute(ctx, call)
	asm.Execute(ctx, call)
	if res := asm.Execute(ctx, call); !res.IsError {
		t.Fatal("third identical call was executed with default settings")
	}
	if got := len(rec.Calls()); got != 2 {
		t.Fatalf("tool executed %d times, want 2", got)
	}
}

// TestRepeatGuardIgnoresInterleavedWork pins that only back-to-back
// repeats count: a read → edit → read → edit workflow issues each call
// again and again, but never twice in a row, so nothing may be refused.
// This is the shape the guard must leave alone — the trade-off is that
// an agent alternating two identical calls in a loop is not caught.
func TestRepeatGuardIgnoresInterleavedWork(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":2}`)
	ctx := repeatCtx(asm)
	for i := 0; i < 5; i++ {
		if res := asm.Execute(ctx, probeCall(`{"path":"a.txt"}`)); res.IsError {
			t.Fatalf("read %d blocked: %s", i+1, res.Content.Text())
		}
		if res := asm.Execute(ctx, probeCall(
			`{"path":"a.txt","content":"fix"}`)); res.IsError {
			t.Fatalf("edit %d blocked: %s", i+1, res.Content.Text())
		}
	}
	if got := len(rec.Calls()); got != 10 {
		t.Fatalf("tool executed %d times, want 10", got)
	}
}

// TestRepeatGuardNormalizesArguments pins that a re-serialization of
// the same object (key order, whitespace) is the same call.
func TestRepeatGuardNormalizesArguments(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":3}`)
	ctx := repeatCtx(asm)
	asm.Execute(ctx, probeCall(`{"a":1,"b":2}`))
	asm.Execute(ctx, probeCall(`{"b":2,"a":1}`))
	res := asm.Execute(ctx, probeCall("{ \"a\": 1, \"b\": 2 }"))
	if !res.IsError {
		t.Fatal("reordered arguments escaped the guard")
	}
	if got := len(rec.Calls()); got != 2 {
		t.Fatalf("tool executed %d times, want 2", got)
	}
}

// TestRepeatGuardIsolatesRuns pins the per-session scope: one run's
// repeats never block another run's first call.
func TestRepeatGuardIsolatesRuns(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":2}`)
	first := repeatCtx(asm)
	second := repeatCtx(asm)
	call := probeCall(`{"path":"a.txt"}`)
	asm.Execute(first, call)
	if res := asm.Execute(first, call); !res.IsError {
		t.Fatal("second call in the same run was not blocked")
	}
	if res := asm.Execute(second, call); res.IsError {
		t.Fatalf("another run's first call was blocked: %s", res.Content.Text())
	}
	if got := len(rec.Calls()); got != 2 {
		t.Fatalf("tool executed %d times, want 2", got)
	}
}

// TestRepeatGuardStreakResetsOnADifferentCall pins the escape: a call
// that is not the repeat resets the streak, so the identical call runs
// again afterwards.
func TestRepeatGuardStreakResetsOnADifferentCall(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":2}`)
	ctx := repeatCtx(asm)
	call := probeCall(`{"path":"a.txt"}`)
	asm.Execute(ctx, call)
	if res := asm.Execute(ctx, call); !res.IsError {
		t.Fatal("second identical call was not blocked")
	}
	// One different call in between makes the retry a new attempt.
	if res := asm.Execute(ctx, probeCall(`{"path":"b.txt"}`)); res.IsError {
		t.Fatalf("different call blocked: %s", res.Content.Text())
	}
	if res := asm.Execute(ctx, call); res.IsError {
		t.Fatalf("call stayed blocked after a different call: %s", res.Content.Text())
	}
	if got := len(rec.Calls()); got != 3 {
		t.Fatalf("tool executed %d times, want 3", got)
	}
}

// TestRepeatGuardExemptsNamedTools pins the polling escape hatch.
func TestRepeatGuardExemptsNamedTools(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":2,"exempt":["probe"]}`)
	ctx := repeatCtx(asm)
	call := probeCall(`{"path":"a.txt"}`)
	for i := 0; i < 4; i++ {
		if res := asm.Execute(ctx, call); res.IsError {
			t.Fatalf("exempt tool blocked on call %d: %s", i+1, res.Content.Text())
		}
	}
	if got := len(rec.Calls()); got != 4 {
		t.Fatalf("tool executed %d times, want 4", got)
	}
}

// TestRepeatGuardDisabled pins that a deployment can turn the guard
// off entirely.
func TestRepeatGuardDisabled(t *testing.T) {
	asm, rec := repeatAssembly(t, `{"enabled":false}`)
	ctx := repeatCtx(asm)
	call := probeCall(`{"path":"a.txt"}`)
	for i := 0; i < 4; i++ {
		if res := asm.Execute(ctx, call); res.IsError {
			t.Fatalf("disabled guard blocked call %d", i+1)
		}
	}
	if got := len(rec.Calls()); got != 4 {
		t.Fatalf("tool executed %d times, want 4", got)
	}
}

// TestRepeatGuardWithoutSessionIsInert pins the no-run case: calls
// outside a tool session are not tracked, because a process-wide
// counter would block unrelated conversations against each other.
func TestRepeatGuardWithoutSessionIsInert(t *testing.T) {
	asm, rec := repeatAssembly(t,
		`{"enabled":true,"threshold":2}`)
	ctx := context.Background()
	call := probeCall(`{"path":"a.txt"}`)
	for i := 0; i < 3; i++ {
		if res := asm.Execute(ctx, call); res.IsError {
			t.Fatalf("call %d blocked without a session", i+1)
		}
	}
	if got := len(rec.Calls()); got != 3 {
		t.Fatalf("tool executed %d times, want 3", got)
	}
}

// TestRepeatSettingsValidation pins that unusable numbers are rejected
// at assembly time instead of degrading silently.
func TestRepeatSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		repeat string
	}{
		{name: "negative threshold", repeat: `{"enabled":true,"threshold":-1}`},
		{name: "threshold below two", repeat: `{"enabled":true,"threshold":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := AssemblyFactory{}
			_, err := f.New(context.Background(), resource.Input{
				Settings: []byte(`{"middlewares":{"repeat":` + tc.repeat + `}}`),
				Deps: map[string]any{
					"tool": stubSource{tooltest.NewRecordingTool("probe")},
				},
			})
			if err == nil {
				t.Fatal("invalid repeat settings accepted")
			}
			if !strings.Contains(err.Error(), "repeat.") {
				t.Fatalf("error %q does not name the repeat section", err)
			}
		})
	}
}
