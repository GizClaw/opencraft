package worldstate

import (
	"strings"

	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

// abortedToolResult is the short, provider-valid marker inserted for an
// assistant tool_call that has no result in the retained window,
// mirroring codex-rs' ensure_call_outputs_present.
const abortedToolResult = "aborted"

// rawContext carries one stored conversation message as a real
// message.Message, plus the pairing metadata worldstate needs to keep
// tool results valid when replaying history.
type rawContext struct {
	msg message.Message
	// calls are assistant tool_call ids this message makes available.
	calls []string
	// needs are tool_result call ids this message requires to stay a
	// valid tool message.
	needs []string
}

// rawContextFor lowers one raw memory item into a message.Message.
// Stored copies carry an extra rendered-text part appended at write
// time; it is dropped here so providers receive only the parts their
// wire format accepts:
//   - assistant tool_call messages keep their calls only;
//   - tool messages keep their results only;
//   - plain user/assistant messages stay text.
func rawContextFor(item memory.ContextItem) rawContext {
	role := string(item.MessageRole)
	var rc rawContext
	switch role {
	case "assistant":
		var calls []message.Part
		for _, part := range item.Content.Parts {
			normalized, err := message.NormalizePart(part)
			if err != nil {
				continue
			}
			if value, ok := normalized.(message.ToolCallPart); ok {
				calls = append(calls, value)
				rc.calls = append(rc.calls, value.Call.ID)
			}
		}
		if len(calls) > 0 {
			rc.msg = message.Message{
				Role:    message.RoleAssistant,
				Content: message.Content{Parts: calls},
			}
		} else {
			rc.msg = message.NewTextMessage(message.RoleAssistant, item.Content.Text())
		}
	case "tool":
		var results []message.Part
		for _, part := range item.Content.Parts {
			normalized, err := message.NormalizePart(part)
			if err != nil {
				continue
			}
			if value, ok := normalized.(message.ToolResultPart); ok {
				value.Result = sanitizeToolResult(value.Result)
				results = append(results, value)
				rc.needs = append(rc.needs, value.Result.CallID)
			}
		}
		rc.msg = message.Message{
			Role:    message.RoleTool,
			Content: message.Content{Parts: results},
		}
	case "user":
		rc.msg = message.NewTextMessage(message.RoleUser, item.Content.Text())
	default:
		// Unknown/legacy roles degrade to user text, matching the
		// pre-structured behavior.
		rc.msg = message.NewTextMessage(message.RoleUser, item.Content.Text())
	}
	return rc
}

// sanitizeToolResult replaces an empty aborted/error payload with a
// short marker so the model sees the tool did not produce usable
// output without carrying a giant partial payload.
func sanitizeToolResult(r message.ToolResult) message.ToolResult {
	if r.IsError && strings.TrimSpace(r.Content) == "" {
		r.Content = abortedToolResult
	}
	return r
}

// allAvailable reports whether every required call id was seen in an
// earlier assistant tool_call message of the same batch.
func allAvailable(needs []string, available map[string]bool) bool {
	for _, id := range needs {
		if !available[id] {
			return false
		}
	}
	return true
}

// pendingCall tracks an assistant tool_call that has not yet been
// paired with a tool result in the current batch.
type pendingCall struct {
	id string
	// at is the index of the assistant call message in out.
	at int
}

// normalizeRawItems converts stored raw messages into provider-valid
// message.Message history:
//   - valid tool pairs keep their real roles;
//   - tool results without a matching call in the batch are dropped
//     (orphan outputs, matching codex-rs remove_orphan_outputs);
//   - assistant calls that never receive a result get a synthetic
//     short "aborted" tool message right after the call, matching
//     codex-rs ensure_call_outputs_present.
func normalizeRawItems(items []memory.ContextItem) []message.Message {
	available := map[string]bool{}
	out := make([]message.Message, 0, len(items))
	var pending []pendingCall
	for _, item := range items {
		rc := rawContextFor(item)
		switch rc.msg.Role {
		case message.RoleAssistant:
			if len(rc.calls) == 0 {
				if rc.msg.Content.Text() == "" {
					continue
				}
				out = append(out, rc.msg)
				continue
			}
			for _, id := range rc.calls {
				available[id] = true
				pending = append(pending, pendingCall{id: id, at: len(out)})
			}
			out = append(out, rc.msg)
		case message.RoleTool:
			if len(rc.msg.Content.Parts) == 0 ||
				!allAvailable(rc.needs, available) {
				// Orphan tool output: no matching call in this batch.
				continue
			}
			for _, id := range rc.needs {
				removePending(&pending, id)
			}
			out = append(out, rc.msg)
		default:
			if rc.msg.Content.Text() == "" {
				continue
			}
			out = append(out, rc.msg)
		}
	}

	// Insert synthetic aborted results immediately after their call.
	for i := len(pending) - 1; i >= 0; i-- {
		p := pending[i]
		insertAt := p.at + 1
		if insertAt > len(out) {
			insertAt = len(out)
		}
		synthetic := message.Message{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID:  p.id,
					Content: abortedToolResult,
				}},
			}},
		}
		out = append(out[:insertAt],
			append([]message.Message{synthetic}, out[insertAt:]...)...)
	}
	return out
}

func removePending(pending *[]pendingCall, id string) {
	kept := (*pending)[:0]
	for _, p := range *pending {
		if p.id != id {
			kept = append(kept, p)
		}
	}
	*pending = kept
}

// renderRawSections converts stored raw messages into memory_raw
// sections using the shared normalizeRawItems pipeline.
func renderRawSections(items []memory.ContextItem) []Section {
	out := make([]Section, 0, len(items))
	for _, msg := range normalizeRawItems(items) {
		out = append(out, Section{ID: "memory_raw", Message: msg})
	}
	return out
}

// renderHistoryMessages is renderRawSections' counterpart for
// full-history replay: the same pairing rules apply, and output is the
// message list world.js appends after the world sections.
func renderHistoryMessages(items []memory.ContextItem) []message.Message {
	return normalizeRawItems(items)
}
