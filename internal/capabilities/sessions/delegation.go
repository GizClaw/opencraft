package sessions

import (
	"context"
	"fmt"
	"strings"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

// ModeAttribute is the delegation run-attribute key that carries the
// caller's permission mode to a subagent's run.
//
// It is stamped by the tool assembly at delegate-call time
// (delegation.WithRunAttributes, which the delegate tool copies onto the
// request) and read back by [ModeInitializer] on the delegation side. The
// model cannot write it: run attributes are not tool arguments, and the
// delegate tool's own schema has no field that reaches them.
const ModeAttribute = "oc.sandbox.mode"

// ModeInitializer returns the delegation session initializer that gives a
// subagent session the sandbox mode its caller ran under.
//
// Why a hook and not the request's own fields: a delegated run always
// gets a fresh session id (see [session.Key]), so the settings document
// the sandbox and the tool policies read is empty and the store's default
// is the confined workspace mode. A yolo caller's subagent therefore ran
// confined, and because the caller is the only session with a user behind
// it, every approval the child asked for was refused on the caller's
// behalf — the run stalled instead of working. The mode is per-session
// state core deliberately does not model, so it travels as an ordinary
// run attribute ([ModeAttribute]) and is applied here, before the child's
// turn starts.
//
// The attribute is applied whenever it is present. A grandchild inherits
// the same way, one hop at a time: the assembly resolves the mode again
// for every delegate call, and a call made from inside this child
// resolves the child's own row — which now carries the caller's mode.
// The attribute is not itself a channel down the tree. A delegation that
// carries no attribute (no run info to resolve it from, e.g. an
// automation-driven turn) is left alone: the store default is the right
// answer there, and inventing a mode would widen a run nobody asked to
// widen. An attribute this package cannot read — an unknown mode, or a
// confined mode in a yolo-only build — fails the delegation instead of
// running the child under a wider default: the caller asked for a
// policy, and it must not be silently dropped.
func ModeInitializer(store *Store) sdkdelegation.SessionInitializer {
	if store == nil {
		return nil
	}
	return func(
		ctx context.Context, key session.Key, req sdkdelegation.AsyncRequest,
	) error {
		mode := strings.TrimSpace(req.Request.Attributes[ModeAttribute])
		if mode == "" {
			return nil
		}
		if strings.TrimSpace(key.ContextID) == "" {
			return errdefs.Validationf(
				"sessions: delegated run for %q has no session id to "+
					"apply permission mode %q to", key.AgentID, mode)
		}
		if err := store.SetMode(ctx, key.ContextID, Mode(mode)); err != nil {
			// SetMode already classifies its own refusals (an unknown
			// mode, a confined mode in a yolo-only build) as
			// validation; wrapping keeps that class and the chain, so
			// a storage failure stays retryable instead of being
			// reported as a bad argument.
			return fmt.Errorf(
				"sessions: delegated run for %q: apply permission mode "+
					"%q: %w", key.AgentID, mode, err)
		}
		return nil
	}
}
