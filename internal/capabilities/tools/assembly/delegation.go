package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

// delegatedToolID is the delegate tool's id. The policy is enforced on
// the call, not on the tool's schema: hiding a target from the listing
// is a hint, and a model that names a hidden target anyway must be
// refused, not trusted.
const delegatedToolID = "delegate"

// delegationMiddleware refuses delegate calls whose target the policy
// does not allow.
//
// Why here: the target directory is a core concrete type
// (*delegation.LocalDirectory) that the service resolves through, so
// filtering there would mean changing flowcraft. The tool call is the
// app-owned seam with the same authority — nothing reaches the
// delegation service without passing it — and a refused call costs one
// tool result instead of a running subagent.
func delegationMiddleware(p *subagents.Policy) tool.Middleware {
	if p.Empty() {
		return nil
	}
	return func(next tool.Dispatch) tool.Dispatch {
		return func(
			ctx context.Context, call message.ToolCall,
		) message.ToolResult {
			if call.Name != delegatedToolID {
				return next(ctx, call)
			}
			target, ok := delegationTarget(string(call.Arguments))
			if !ok || p.Allows(target) {
				// An unreadable argument is left to the tool itself:
				// shaping the error there keeps one owner per message.
				return next(ctx, call)
			}
			return message.ToolResult{
				CallID:  call.ID,
				IsError: true,
				Content: message.NewTextContent(fmt.Sprintf(
					"delegation to %q is refused by the delegation policy: %s",
					target, p.Describe())),
			}
		}
	}
}

// delegationTarget reads the target out of a delegate call's
// arguments. It tolerates the empty object the model sends while it is
// still filling the call in: only a present, non-blank target is worth
// judging.
func delegationTarget(arguments string) (string, bool) {
	if strings.TrimSpace(arguments) == "" {
		return "", false
	}
	var args struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", false
	}
	target := strings.TrimSpace(args.Target)
	return target, target != ""
}

// delegationModeMiddleware stamps the caller's permission mode onto every
// delegate call as a run attribute, so the subagent's own session can
// start under it (see sessions.ModeInitializer).
//
// Why here: the tool call is the last seam that still sees the caller's
// context — the delegate tool copies the attributes stamped there onto
// its request, and core deliberately does not propagate a parent's
// request attributes on its own (they carry run-scoped payloads, such as
// checkpoint recovery blobs, that must not leak into a subagent). The
// delegate tool's arguments are the model's, so a mode that came from
// them would let a subagent widen its own policy; this one comes from the
// caller's session row instead.
//
// The mode is layered onto whatever a deployment stamped further out
// rather than replacing it: run attributes are the channel for any
// caller-declared execution policy, and dropping an outer producer's
// keys would break it without a word.
func delegationModeMiddleware(store *sessions.Store) tool.Middleware {
	if store == nil {
		return nil
	}
	return func(next tool.Dispatch) tool.Dispatch {
		return func(
			ctx context.Context, call message.ToolCall,
		) message.ToolResult {
			if call.Name != delegatedToolID {
				return next(ctx, call)
			}
			mode, ok, err := sessions.ModeFromContext(ctx, store)
			if err != nil {
				// The caller has a session and the store cannot say what
				// mode it is in. Delegating anyway would hand the child
				// the confined default, which is the failure this
				// middleware exists to prevent, so the call fails
				// instead.
				return message.ToolResult{
					CallID:  call.ID,
					IsError: true,
					Content: message.NewTextContent(fmt.Sprintf(
						"cannot read the caller's permission mode, so the "+
							"delegation would run under the wrong policy: %v",
						err)),
				}
			}
			if !ok {
				return next(ctx, call)
			}
			attributes, _ := sdkdelegation.RunAttributesFromContext(ctx)
			if attributes == nil {
				attributes = make(map[string]string, 1)
			}
			attributes[sessions.ModeAttribute] = string(mode)
			return next(sdkdelegation.WithRunAttributes(ctx, attributes), call)
		}
	}
}
