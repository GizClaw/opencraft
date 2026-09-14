package host

import (
	"errors"
	"fmt"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
)

func TestClassifyRunErrorReadsTheEngineTypes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resErr    error
		waitErr   error
		wantCause string
		wantKind  string
	}{
		{
			name: "user cancel",
			resErr: fmt.Errorf("graph %q node %q: %w", "assistant", "llm",
				agent.Interrupted(agent.Interrupt{
					Cause: agent.CauseUserCancel,
				})),
			wantCause: "user_cancel",
		},
		{
			name: "barge-in with detail",
			resErr: agent.Interrupted(agent.Interrupt{
				Cause:  agent.CauseUserInput,
				Detail: "new message arrived",
			}),
			wantCause: "user_input",
		},
		{
			name: "provider failure kind",
			resErr: fmt.Errorf("graph %q node %q: %w", "assistant", "llm",
				&inference.Error{Kind: inference.ProviderFailure}),
			wantKind: "provider_failure",
		},
		{
			name:    "the wait error classifies when the result carries none",
			waitErr: agent.Interrupted(agent.Interrupt{Cause: agent.CauseHostShutdown}),
			// The result error wins when both exist, so this case pins
			// the fallback.
			wantCause: "host_shutdown",
		},
		{
			name:    "the result error wins over the wait error",
			resErr:  agent.Interrupted(agent.Interrupt{Cause: agent.CauseUserCancel}),
			waitErr: errors.New("wait: context canceled"),
			// The engine's own classification is the authoritative one.
			wantCause: "user_cancel",
		},
		{
			name:      "plain failure carries no class",
			resErr:    errors.New("engine blew up"),
			wantCause: "",
			wantKind:  "",
		},
		{
			name: "unknown cause stays empty",
			resErr: agent.Interrupted(agent.Interrupt{
				Detail: "something happened",
			}),
			wantCause: "",
		},
	} {
		class := ClassifyRunError(tc.resErr, tc.waitErr)
		if class.InterruptCause != tc.wantCause || class.ErrorKind != tc.wantKind {
			t.Errorf("%s: class = %+v, want cause %q kind %q",
				tc.name, class, tc.wantCause, tc.wantKind)
		}
	}
}

func TestClassifyRunErrorWithoutError(t *testing.T) {
	if class := ClassifyRunError(nil, nil); class != (TurnErrorClass{}) {
		t.Fatalf("class = %+v, want zero", class)
	}
}
