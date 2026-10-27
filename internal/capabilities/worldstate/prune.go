package worldstate

import (
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"
)

// The model reads the conversation through the replay, and nothing bounds what
// a tool wrote: a conversation that ran `go test ./...` or read a large file
// carries every byte of every result forever. Once the model has worked past a
// result it gains nothing from keeping it whole, but it still occupies the
// window — and the window is what decides when the graph folds, so an
// unbounded tail folds more often than the conversation needs.
//
// The bound is applied to the view, never to the archive: the transcript keeps
// the full output (the reader can still open it in the transcript card), and
// the rule is deterministic — same rows in, same window out — which is what the
// fold's fixed point depends on.
const (
	// keepToolResultsVerbatim is how many of the newest results stay whole: the
	// model is working with those, and truncating the output it is reading
	// would make it act on a guess.
	keepToolResultsVerbatim = 8
	// maxOldToolResultChars bounds an older result in the window. Head and tail
	// both survive: a tool prints its summary first and its failure last.
	maxOldToolResultChars = 2000
)

// pruneOldToolResults shortens the tool results that are no longer among the
// newest ones. Items are in transcript order, so "newest" is counted from the
// end rather than read from a timestamp.
func pruneOldToolResults(items []memory.ContextItem) []memory.ContextItem {
	seen := 0
	for i := len(items) - 1; i >= 0; i-- {
		if !hasToolOutput(items[i].Content) {
			continue
		}
		seen++
		if seen <= keepToolResultsVerbatim {
			continue
		}
		items[i].Content = pruneContent(items[i].Content)
	}
	return items
}

// hasToolOutput reports whether the message carries a tool result, which is the
// only shape this rule touches: an assistant's own text is part of the
// conversation, not output the model can re-read elsewhere.
func hasToolOutput(content message.Content) bool {
	for _, part := range content.Parts {
		if _, ok := part.(message.ToolResultPart); ok {
			return true
		}
	}
	return false
}

// pruneContent rebuilds a message's parts, shortening text and replacing inline
// media. It always allocates: the items it is given may be shared with the
// replay cache, and a "prune" that also shortened the cache's copy would change
// what a later turn reads.
func pruneContent(content message.Content) message.Content {
	parts := make([]message.Part, 0, len(content.Parts))
	for _, part := range content.Parts {
		switch typed := part.(type) {
		case message.ToolResultPart:
			result := typed.Result.Clone()
			result.Content = pruneContent(result.Content)
			parts = append(parts, message.ToolResultPart{Result: result})
		case message.TextPart:
			parts = append(parts, message.TextPart{Text: pruneText(typed.Text)})
		case message.ImagePart:
			parts = append(parts,
				message.TextPart{Text: "[image omitted from an older tool result]"})
		default:
			parts = append(parts, part)
		}
	}
	return message.Content{Parts: parts}
}

// pruneText keeps the head and the tail of an oversized result and says how
// much it dropped, so the model knows it is reading an excerpt and where the
// rest lives.
func pruneText(text string) string {
	runes := []rune(text)
	if len(runes) <= maxOldToolResultChars {
		return text
	}
	half := maxOldToolResultChars / 2
	head := strings.TrimRight(string(runes[:half]), "\n")
	tail := strings.TrimLeft(string(runes[len(runes)-half:]), "\n")
	return fmt.Sprintf(
		"%s\n…\n%s\n[tool output truncated: %d of %d characters elided; the transcript keeps all of it]",
		head, tail, len(runes)-2*half, len(runes))
}
