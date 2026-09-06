package worldstate

import (
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

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
	// fallbackText preserves the original rendered text of a tool
	// message whose pairing is lost (or whose result part is absent),
	// so it can degrade to a user-role text message.
	fallbackText string
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
	rc := rawContext{fallbackText: item.Content.Text()}
	switch role {
	case "assistant":
		var calls []message.Part
		for _, part := range item.Content.Parts {
			normalized, err := message.NormalizePart(part)
			if err != nil {
				continue
			}
			switch value := normalized.(type) {
			case message.ToolCallPart:
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

// finalizeRaw applies pair tracking: assistant calls make their ids
// available, and a tool message keeps its tool role only when its
// result ids were seen earlier in the same batch. Unpaired or empty
// tool messages fall back to user text.
func finalizeRaw(rc rawContext, available map[string]bool) (message.Message, bool) {
	msg := rc.msg
	switch msg.Role {
	case message.RoleAssistant:
		for _, id := range rc.calls {
			available[id] = true
		}
	case message.RoleTool:
		if len(msg.Content.Parts) == 0 || !allAvailable(rc.needs, available) {
			if rc.fallbackText == "" {
				return message.Message{}, false
			}
			return message.NewTextMessage(message.RoleUser, rc.fallbackText), true
		}
	}
	if len(msg.Content.Parts) == 0 && msg.Content.Text() == "" {
		return message.Message{}, false
	}
	return msg, true
}

// renderRawSections converts stored raw messages into memory_raw
// sections that carry the original message.Message, preserving valid
// assistant tool_call / tool result pairs.
func renderRawSections(items []memory.ContextItem) []Section {
	available := map[string]bool{}
	out := make([]Section, 0, len(items))
	for _, item := range items {
		msg, ok := finalizeRaw(rawContextFor(item), available)
		if !ok {
			continue
		}
		out = append(out, Section{ID: "memory_raw", Message: msg})
	}
	return out
}

// renderHistoryMessages is renderRawSections' counterpart for
// full-history replay: the same pairing rules apply, and output is the
// message list world.js appends after the world sections.
func renderHistoryMessages(items []memory.ContextItem) []message.Message {
	available := map[string]bool{}
	out := make([]message.Message, 0, len(items))
	for _, item := range items {
		msg, ok := finalizeRaw(rawContextFor(item), available)
		if !ok {
			continue
		}
		out = append(out, msg)
	}
	return out
}
