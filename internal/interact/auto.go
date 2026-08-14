package interact

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// Auto is a non-interactive Backend for headless/CLI runs: every
// prompt fails with NotAvailable, and the Broker converts that into an
// empty cancelled reply so the agent never blocks on user input.
type Auto struct{}

// Ask implements Backend.
func (Auto) Ask(context.Context, Spec) (Reply, error) {
	return Reply{}, errdefs.NotAvailablef(
		"opencraft: no interactive user backend")
}
