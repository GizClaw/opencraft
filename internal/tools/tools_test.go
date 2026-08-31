package tools

import (
	"context"
	"testing"

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

func TestExecToolListPlatformGate(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "freebsd", "windows"} {
		names := toolNames(execToolList(stubRunner{}, goos))
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
