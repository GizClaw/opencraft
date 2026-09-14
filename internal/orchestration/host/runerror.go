package host

import (
	"errors"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
)

// TurnErrorClass is the machine-readable half of one run's terminal
// error: why an interrupted turn stopped, and which class of failure it
// was. Both are stable enums the engine already carries as types, so a
// consumer never has to parse the rendered error text — that text is
// prose owned by flowcraft and changes with its wording.
type TurnErrorClass struct {
	// InterruptCause is the engine's interrupt cause
	// (user_cancel/user_input/host_shutdown/custom). Empty when the turn
	// did not end as an interrupt, including the engine's zero cause.
	InterruptCause string
	// ErrorKind is the inference error kind
	// (provider_failure/invalid_provider_response/...). Empty when the
	// failure is not an inference error (graph wiring, tool code, ...).
	ErrorKind string
}

// ClassifyRunError extracts the structured class of one run's terminal
// error. resErr is the engine's result error; waitErr is what the run
// wait returned — the same precedence the run records elsewhere, so the
// class and the rendered text always come from the same error.
func ClassifyRunError(resErr, waitErr error) TurnErrorClass {
	err := resErr
	if err == nil {
		err = waitErr
	}
	var class TurnErrorClass
	if err == nil {
		return class
	}
	var interrupted agent.InterruptedError
	if errors.As(err, &interrupted) {
		class.InterruptCause = string(interrupted.Cause)
	}
	var infErr *inference.Error
	if errors.As(err, &infErr) && infErr != nil {
		class.ErrorKind = string(infErr.Kind)
	}
	return class
}
