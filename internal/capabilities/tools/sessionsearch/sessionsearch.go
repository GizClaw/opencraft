// Package sessionsearch provides the session_search tool: full-text
// recall over this workspace's archived conversations, so a decision
// made in an earlier session can be found again instead of repeated.
//
// The tool is a thin presentation layer over the session store's
// message search (capabilities/sessions → state): the store owns the
// index, the query and the ranking, this package owns the model-facing
// envelope.
package sessionsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// Name is the canonical session_search tool name.
const Name = "session_search"

// defaultLimit / maxLimit bound one call's hit list. The store clamps
// the same way; repeating the bounds here only words them for the
// model.
const (
	defaultLimit = 8
	maxLimit     = 10
)

// noResultsNote replaces an empty hit list with an explicit hint, so
// the model retries with different words instead of treating "[]" as
// an error.
const noResultsNote = "no matches in this workspace's past sessions; " +
	"try different keywords or a distinctive phrase"

// shortQueryNote explains the substring fallback: the trigram index
// cannot match a term shorter than three characters, so the query ran
// as a plain scan (which is slower and unranked).
const shortQueryNote = "query terms shorter than three characters were " +
	"matched by substring scan, not the ranked index"

// Searcher is the session-store read surface the tool needs.
// *sessions.Store implements it.
type Searcher interface {
	SearchMessages(
		ctx context.Context, query string, opts sessions.SearchOptions,
	) (sessions.SearchResult, error)
}

// Tool is the LLM-callable session_search tool.
type Tool struct {
	store Searcher
}

var _ tool.Tool = (*Tool)(nil)

// New builds the tool over a session store.
func New(store Searcher) *Tool {
	return &Tool{store: store}
}

// Definition implements tool.Tool. The tool stays discoverable through
// tool_search (it is not in the always-visible set), so the description
// carries the words a recall request is phrased with: the discovery
// index is BM25 over name and description only.
func (t *Tool) Definition() message.ToolDefinition {
	description := "Search this workspace's past conversations for " +
		"anything said, decided or written in earlier sessions: earlier " +
		"messages, plans, decisions and tool results that are not in the " +
		"current context. Use it when the user recalls something from a " +
		"previous session (\"what did we decide about X\", \"where did I " +
		"write down Y\", \"that session last week\") or when you need " +
		"context that predates this conversation. Matching is literal " +
		"substring search over every archived message of every session, " +
		"including tool results; results carry one snippet per session, " +
		"best first. This searches this machine's conversation history " +
		"only — use web_search for the internet."
	return message.DefineSchema(
		Name,
		description,
		message.ToolProperty("query", "string",
			"Keywords or a distinctive phrase to find (required). "+
				"Prefer several distinctive terms over a sentence."),
		message.ToolPropertyWithDefault("limit", "integer",
			fmt.Sprintf(
				"Maximum number of sessions to return (1-%d, default %d).",
				maxLimit, defaultLimit),
			defaultLimit),
	).Required("query").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.Tool: the search is a local index read with
// no side effects.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{}
}

// Execute implements tool.Tool. The result is one text part carrying
// the JSON envelope.
func (t *Tool) Execute(
	ctx context.Context, arguments string,
) (message.Content, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return message.Content{}, errdefs.Validationf(
			"session_search: invalid arguments: %v", err)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return message.Content{}, errdefs.Validationf(
			"session_search: query is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	res, err := t.store.SearchMessages(ctx, query, sessions.SearchOptions{
		Limit:    limit,
		Collapse: true,
	})
	if err != nil {
		return message.Content{}, err
	}
	out := envelope{
		Query:     query,
		Hits:      make([]hit, 0, len(res.Hits)),
		Truncated: res.Truncated,
	}
	for _, found := range res.Hits {
		out.Hits = append(out.Hits, hit{
			ConversationID: found.ConversationID,
			Title:          found.Title,
			RunID:          found.RunID,
			Role:           found.Role,
			At:             found.At.UTC().Format("2006-01-02T15:04:05Z"),
			Snippet:        found.Snippet,
			Ref:            found.ConversationID + "#" + fmt.Sprint(found.Seq),
		})
	}
	if len(out.Hits) == 0 {
		out.Note = noResultsNote
	} else if res.Substring {
		out.Note = shortQueryNote
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return message.Content{}, errdefs.Internalf(
			"session_search: encode result: %v", err)
	}
	return message.NewTextContent(string(encoded)), nil
}

// envelope is the tool result shape: the query echo first, then the
// ranked hits, so a caller can correlate and a reader can skim.
type envelope struct {
	Query     string `json:"query"`
	Hits      []hit  `json:"hits"`
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// hit is one matched session with its best-matching message. ref is a
// stable locator (session id + message sequence) for a later read of
// the full message.
type hit struct {
	ConversationID string `json:"conversation_id"`
	Title          string `json:"title,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	Role           string `json:"role"`
	At             string `json:"at"`
	Snippet        string `json:"snippet"`
	Ref            string `json:"ref"`
}
