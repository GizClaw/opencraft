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
// resolvable for the lifetime of a turn, returning the release
// function the caller defers. The sink itself is wrapped so the
// exporter can recognize it: a plain closure carries no durable
// destination, which is exactly what "no target" means.
func (c *Core) RegisterConversationSink(
	conversationID string, sink agent.StreamSink,
) func() {
	if sink == nil {
		return func() {}
	}
	targets := c.StreamTargets()
	return targets.Register(conversationID, subagents.Wrap(conversationID, sink))
}
