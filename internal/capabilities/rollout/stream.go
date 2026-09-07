package rollout

import (
	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// ItemEventsFromStream converts one stream delta into the rollout item
// events it represents. Text and reasoning parts are not emitted here:
// callers buffer them until StreamDeltaFinish so an assistant message
// is written once, in order (reasoning first, text second).
func ItemEventsFromStream(
	conversationID, runID string,
	delta agent.StreamDeltaPayload,
) []Event {
	if delta.Type != agent.StreamDeltaPart {
		return nil
	}
	var out []Event
	switch p := delta.Part.(type) {
	case message.ToolCallPart:
		out = append(out, Event{
			Type:           TypeItemToolCall,
			ConversationID: conversationID,
			RunID:          runID,
			ItemID:         p.Call.ID,
			Tool:           p.Call.Name,
			CallID:         p.Call.ID,
			Arguments:      p.Call.Arguments,
		})
	case message.ToolResultPart:
		out = append(out, Event{
			Type:           TypeItemToolResult,
			ConversationID: conversationID,
			RunID:          runID,
			CallID:         p.Result.CallID,
			Content:        p.Result.Content,
			IsError:        p.Result.IsError,
		})
	}
	return out
}

// FlushItemEvents builds the terminal reasoning / assistant-message
// events from the text buffered during one stream finish.
func FlushItemEvents(
	conversationID, runID, reasoning, text string,
) []Event {
	var out []Event
	if reasoning != "" {
		out = append(out, Event{
			Type:           TypeItemReasoning,
			ConversationID: conversationID,
			RunID:          runID,
			Content:        reasoning,
		})
	}
	if text != "" {
		out = append(out, Event{
			Type:           TypeItemAssistantMsg,
			ConversationID: conversationID,
			RunID:          runID,
			Content:        text,
		})
	}
	return out
}
