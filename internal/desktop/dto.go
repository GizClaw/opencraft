package desktop

import (
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/agents"
	"github.com/GizClaw/opencraft/internal/config"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// UIEvent is the envelope pushed to the frontend on the single
// "opencraft:ui" channel.
type UIEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// ConfigStatus describes the application configuration state.
type ConfigStatus struct {
	Needed           bool   `json:"needed"`
	DefaultModel     string `json:"default_model"`
	DefaultReasoning bool   `json:"default_reasoning"`
	WorkDir          string `json:"work_dir"`
	UserDir          string `json:"user_dir"`
	Version          string `json:"version"`
	Agents           int    `json:"agents"`
}

// StreamEvent carries one stream delta plus the run it belongs to, so
// the frontend can route output to the right conversation when
// several turns run in parallel.
type StreamEvent struct {
	RunID          string                   `json:"run_id"`
	ConversationID string                   `json:"conversation_id,omitempty"`
	Delta          agent.StreamDeltaPayload `json:"delta"`
}

// StartTurnRequest carries the explicit session context for a turn, so
// sending never depends on whichever conversation the app currently
// has selected in the UI.
type StartTurnRequest struct {
	ContextID string          `json:"context_id"`
	Message   message.Message `json:"message"`
}

// SessionSnapshot is the authoritative result of selecting a session:
// the id plus the settings that apply to its next turn.
type SessionSnapshot struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Think     string `json:"think"`
	Model     string `json:"model"`
}

// SessionTurnDTO is the wire form of one archived turn: its messages
// plus the artifacts the turn produced. Times are RFC3339 strings so
// the Wails model generator never sees time.Time. RequestedAt is when
// the user's message was accepted and StartedAt is when the agent
// execution began; older archives fall back to At on the Go side.
type SessionTurnDTO struct {
	Seq         int                   `json:"seq"`
	At          string                `json:"at"`
	RequestedAt string                `json:"requested_at,omitempty"`
	StartedAt   string                `json:"started_at,omitempty"`
	FinishedAt  string                `json:"finished_at,omitempty"`
	DurationMs  int64                 `json:"duration_ms,omitempty"`
	Messages    []message.Message     `json:"messages"`
	Artifacts   []ocsessions.Artifact `json:"artifacts,omitempty"`
}

// ProviderView is one entry of the provider catalog.
type ProviderView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultModel  string `json:"default_model"`
	EnvVar        string `json:"env_var"`
	API           string `json:"api"`
	Azure         bool   `json:"azure"`
	ModelEndpoint bool   `json:"model_endpoint"`
}

// ModelView is one model exposed by an inference instance. Capabilities
// are expressed as canonical content kinds so no modal (text/image/
// audio/video generation, multimodal input) is lost across the
// frontend boundary.
type ModelView struct {
	Name               string            `json:"name"`
	Kind               string            `json:"kind,omitempty"`
	Inputs             []string          `json:"inputs"`
	Outputs            []string          `json:"outputs"`
	Reasoning          string            `json:"reasoning"`
	ReasoningEffortMap map[string]string `json:"reasoning_effort_map,omitempty"`
	EffortNone         bool              `json:"effort_none,omitempty"`
	Dimensions         bool              `json:"dimensions,omitempty"`
	WebSearch          bool              `json:"web_search"`
	Endpoint           string            `json:"endpoint"`
}

// ProviderInstance is one inference instance from the frontend.
// Enabled instances appear in router priority order; disabled ones are
// kept so re-enabling needs no re-entry.
type ProviderInstance struct {
	StableID string `json:"stable_id"` // persisted identity; "" on newly added rows
	Type     string `json:"type"`      // catalog type: deepseek | openai | ...
	Name     string `json:"name"`      // display label
	API      string `json:"api"`       // responses | chat (openai only)
	// Key carries a new literal key from the settings page; an empty
	// value means "keep the stored key". The backend never returns the
	// stored secret through this field.
	Key    string `json:"key"`
	KeySet bool   `json:"key_set"` // config-time: a key is already stored
	KeyEnv bool   `json:"key_env"`
	// KeyKeychain reports that the key lives in the OS credential
	// store (0600 files) rather than the config.
	KeyKeychain bool        `json:"key_keychain"`
	Models      []ModelView `json:"models"`
	Endpoint    string      `json:"endpoint"`
	Enabled     bool        `json:"enabled"`
	// Managed marks a deployment owned by a capability plugin: the
	// settings page locks its content and the save path restores any
	// edit/removal instead of applying it.
	Managed bool `json:"managed"`
}

// InferenceRequest is the full inference configuration payload from
// the settings page.
type InferenceRequest struct {
	Instances []ProviderInstance `json:"instances"`
}

// ConfigState describes the currently configured inference wiring so
// the config page can prefill edits instead of starting blank.
type ConfigState struct {
	Instances []ProviderInstance `json:"instances"` // enabled first, in router order
	Model     string             `json:"model"`     // current default model
}

// ModelUsageStat aggregates one model's usage across all workspaces
// and sessions.
type ModelUsageStat struct {
	Model           string `json:"model"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	LatencyMs       int64  `json:"latency_ms"`
	Workspaces      int    `json:"workspaces"`
	Sessions        int    `json:"sessions"`
	UpdatedAt       string `json:"updated_at"`
}

// UsagePoint is one time-bucketed usage sample for a model. Time is an
// RFC3339 UTC hour for hour granularity, or a local YYYY-MM-DD date for
// day granularity.
type UsagePoint struct {
	Time            string `json:"time"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}

// PatchLineDTO is one rendered diff line with old/new line numbers.
type PatchLineDTO struct {
	Kind   string `json:"kind"` // context | add | delete
	OldNum int    `json:"old_num,omitempty"`
	NewNum int    `json:"new_num,omitempty"`
	Text   string `json:"text"`
}

// PatchFileDTO is one file's rendered diff (git-style, with line
// numbers computed against the current file content).
type PatchFileDTO struct {
	Path    string         `json:"path"`
	Action  string         `json:"action"` // add | update | delete
	Added   int            `json:"added"`
	Removed int            `json:"removed"`
	Lines   []PatchLineDTO `json:"lines"`
}

// ModelOption is one selectable per-conversation model hint. ID is the
// "provider/name" value the router's model_hint consumes; Label is the
// human-facing description.
type ModelOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Reasoning bool   `json:"reasoning"`
}

// SessionMeta is one stored conversation for the sessions list.
type SessionMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   string `json:"created_at"` // RFC3339 UTC
	UpdatedAt   string `json:"updated_at"` // RFC3339 UTC
	Messages    int    `json:"messages"`
	TotalTokens int64  `json:"total_tokens"`
}

// SessionImportDTO reports a completed session import to the UI.
type SessionImportDTO struct {
	SessionID string `json:"session_id"`
	Messages  int    `json:"messages"`
	Turns     int    `json:"turns"`
}

// SkillDTO is one discovered skill for the config page.
type SkillDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Path        string `json:"path"`
	// PluginID/PluginName are set when the skill is contributed by an
	// installed plugin. Plugin skills are read-only from this page.
	PluginID   string `json:"plugin_id,omitempty"`
	PluginName string `json:"plugin_name,omitempty"`
}

// MCPStatusDTO is the UI snapshot of one MCP server's connection state.
type MCPStatusDTO struct {
	Name   string `json:"name"`
	Status string `json:"status"` // connected | connecting | error
	Error  string `json:"error,omitempty"`
}

// KanbanCard is the UI snapshot of one delegation board card.
type KanbanCard struct {
	ID          string `json:"id"`
	Producer    string `json:"producer,omitempty"`
	Consumer    string `json:"consumer,omitempty"`
	Status      string `json:"status"`
	Target      string `json:"target"`
	Input       string `json:"input,omitempty"`
	Output      string `json:"output,omitempty"`
	Caller      string `json:"caller,omitempty"`
	Depth       int    `json:"depth"`
	Error       string `json:"error,omitempty"`
	RunID       string `json:"run_id,omitempty"`        // subagent run id
	ParentRunID string `json:"parent_run_id,omitempty"` // caller run id
	CallID      string `json:"call_id,omitempty"`       // delegate tool call id
	CreatedAt   string `json:"created_at"`              // RFC3339 UTC
	UpdatedAt   string `json:"updated_at"`              // RFC3339 UTC
}

// TurnStart identifies one started turn. RequestedAt is when the
// backend accepted the user's request and StartedAt is when agent
// execution began; both are RFC3339 UTC strings.
type TurnStart struct {
	RunID       string `json:"run_id"`
	ContextID   string `json:"context_id"`
	RequestedAt string `json:"requested_at,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
}

// TurnEnd reports a turn's terminal state.
type TurnEnd struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	// Output is the run's final assistant text (bounded), used by
	// automation notifications outside the open workspace where the
	// frontend has no streamed transcript to build the snippet from.
	Output string `json:"output,omitempty"`
	// Notify lets an automation task suppress the system notification
	// for this turn (nil = notify, the default for user turns).
	Notify *bool `json:"notify,omitempty"`
}

// ReplyRequest is a user answer to one interaction.
type ReplyRequest struct {
	Text    string   `json:"text"`
	Option  *string  `json:"option"`
	Options []string `json:"options"`
	Cancel  bool     `json:"cancel"`
}

// AttachmentDTO is one local attachment the frontend previews (input
// composer) or a resumed session renders. DataURL carries the image
// bytes as a data: URI (WKWebView cannot load file:// directly); file
// attachments return metadata only.
type AttachmentDTO struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
	DataURL   string `json:"data_url,omitempty"`
}

// InteractDTO is the rendered form of one runtime.Spec.
type InteractDTO struct {
	ID             string            `json:"id"`
	RunID          string            `json:"run_id"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Kind           string            `json:"kind"`
	Title          string            `json:"title"`
	Body           []json.RawMessage `json:"body"`
	Options        []OptionDTO       `json:"options"`
	Multi          bool              `json:"multi"`
	AllowOther     bool              `json:"allow_other"`
	Source         string            `json:"source"`
}

// OptionDTO is one selectable choice.
type OptionDTO struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// StatusDTO updates the status bar.
type StatusDTO struct {
	Text string `json:"text"`
	Busy bool   `json:"busy"`
}

// UsageDTO reports one inference usage report.
type UsageDTO struct {
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	LatencyMs        int64  `json:"latency_ms"`
}

// ResolvedDTO notifies the UI that a pending interaction was resolved
// externally.
type ResolvedDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// FileNode is one directory entry in the workspace panel.
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"is_dir"`
	Size     int64      `json:"size,omitempty"`
	Children []FileNode `json:"children,omitempty"`
}

// AgentSummary is the persisted subagent list entry.
type AgentSummary = agents.Summary

// GraphNode is the UI snapshot of one subagent graph node. Config is
// kept as raw JSON so the editor round-trips node-type-specific knobs.
type GraphNode struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

// GraphEdge is one directed transition in a subagent graph.
type GraphEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

// Graph is the parsed flowcraft graph definition of one subagent.
type Graph struct {
	Name  string      `json:"name"`
	Entry string      `json:"entry"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// AgentDetail is the editable view of one persisted subagent: its
// identity plus the parsed graph definition it runs on.
type AgentDetail struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Graph       Graph  `json:"graph"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// AgentUpdateResult reports one persisted subagent update. Timestamps
// are RFC3339 strings so the Wails binding surface never exposes
// time.Time (which its model generator cannot type).
type AgentUpdateResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PersistedTo string `json:"persisted_to"`
	CreatedAt   string `json:"created_at"`
}

// providerByID resolves the catalog entry for one provider id.
func providerByID(id string) (config.Provider, bool) {
	for _, p := range config.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return config.Provider{}, false
}

// marshalParts encodes canonical message parts in their wire form.
func marshalParts(parts []message.Part) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(parts))
	for _, part := range parts {
		raw, err := message.MarshalPart(part)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}
