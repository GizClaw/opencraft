package tools

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

// stubRunner is a minimal sandbox.Runner for wiring tests: tool
// construction only stores the runner, so the stub never has to spawn
// anything.
type stubRunner struct{}

func (stubRunner) Close() error                           { return nil }
func (stubRunner) Capabilities() coresandbox.Capabilities { return coresandbox.Capabilities{} }
func (stubRunner) Start(context.Context, coresandbox.SessionSpec) (coresandbox.Session, error) {
	return nil, nil
}
func (stubRunner) List(context.Context) ([]coresandbox.SessionInfo, error) { return nil, nil }
func (stubRunner) Terminate(context.Context, string) error                 { return nil }

func toolNames(ts toolList) []string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Definition().Name)
	}
	return names
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestWebSearchSourceFactory covers the tool.Source contract: the
// factory builds one web_search tool from its settings, and the
// enabled:false switch removes it without failing the build.
func TestWebSearchSourceFactory(t *testing.T) {
	reg := resource.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	factory, ok := reg.Lookup("tool.Source", "opencraft/websearch")
	if !ok {
		t.Fatal("websearch factory is not registered")
	}
	built, err := factory.New(context.Background(), resource.Input{
		Settings: []byte(`{"enabled":true,"max_results":3}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	src, ok := built.(toolList)
	if !ok {
		t.Fatalf("built = %T, want toolList", built)
	}
	if names := toolNames(src); len(names) != 1 || names[0] != "web_search" {
		t.Fatalf("names = %v", names)
	}

	disabled, err := factory.New(context.Background(), resource.Input{
		Settings: []byte(`{"enabled":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if names := toolNames(disabled.(toolList)); len(names) != 0 {
		t.Fatalf("disabled names = %v", names)
	}
}

func TestExecToolListPlatformGate(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "freebsd", "windows"} {
		names := toolNames(execToolList(stubRunner{}, nil, goos))
		if !contains(names, "exec_command") {
			t.Errorf("%s: exec_command missing from %v", goos, names)
		}
		if goos == "windows" {
			if contains(names, "exec_session") {
				t.Errorf("%s: exec_session must not be offered (got %v)", goos, names)
			}
		} else if !contains(names, "exec_session") {
			t.Errorf("%s: exec_session missing from %v", goos, names)
		}
	}
}
