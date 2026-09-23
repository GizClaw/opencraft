// Delegation stream delivery: the desktop holds one registry of live
// conversation sinks per process, and both halves of flowcraft's
// delegation stream contract are implemented over it. A sink is
// registered while a turn of that conversation runs; the exporter
// describes it as {kind: "conversation", id: <conversationID>} when an
// async delegation crosses the queue, and the resolver materializes
// that description back into the live sink on the worker side.
package core

import (
	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

// StreamTargets returns the process-wide conversation sink registry.
// It is created on first use so a Core assembled by a test has one
// too.
func (c *Core) StreamTargets() *subagents.StreamTargets {
	c.streamTargetsOnce.Do(func() {
		if c.streamTargets == nil {
			c.streamTargets = subagents.NewStreamTargets()
		}
	})
	return c.streamTargets
}

// RegisterConversationSink makes one conversation's live sink
// resolvable for the lifetime of a turn. It returns the sink the
// registry stored — the wrapped, describable form — together with the
// release function the caller defers. The returned sink is the one the
// turn must stream through: the delegation exporter matches by
// capability, so a turn that streams through the bare closure would
// describe nothing and cross-process delivery would silently degrade.
func (c *Core) RegisterConversationSink(
	conversationID string, sink agent.StreamSink,
) (agent.StreamSink, func()) {
	if sink == nil {
		return nil, func() {}
	}
	targets := c.StreamTargets()
	wrapped := subagents.Wrap(conversationID, sink)
	return wrapped, targets.Register(conversationID, wrapped)
}
