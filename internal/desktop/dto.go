package desktop

import (
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/agents"
	"github.com/GizClaw/opencraft/internal/config"
)

// UIEvent is the envelope pushed to the frontend on the single
// "opencraft:ui" channel.
type UIEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// ConfigStatus describes the application configuration state.
type ConfigStatus struct {
	Needed       bool   `json:"needed"`
	DefaultModel string `json:"default_model"`
	WorkDir      string `json:"work_dir"`
	UserDir      string `json:"user_dir"`
	Version      string `json:"version"`
	Agents       int    `json:"agents"`
}

// StreamEvent carries one stream delta plus the run it belongs to, so
// the frontend can route output to the right conversation when
// several turns run in parallel.
type StreamEvent struct {
	RunID string                   `json:"run_id"`
	Delta agent.StreamDeltaPayload `json:"delta"`
}

// ProviderView is one entry of the provider catalog.
type ProviderView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultModel string `json:"default_model"`
	EnvVar       string `json:"env_var"`
	API          string `json:"api"`
	Azure        bool   `json:"azure"`
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
	Key       string `json:"key"`
	KeySet    bool   `json:"key_set"` // config-time: a key is already stored
	KeyEnv    bool   `json:"key_env"`
	Model     string `json:"model"`
	Endpoint  string `json:"endpoint"`
	Vision    bool   `json:"vision"`
	Reasoning string `json:"reasoning"`
	WebSearch bool   `json:"web_search"`
	Enabled   bool   `json:"enabled"`
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
	ID    string `json:"id"`
	Label string `json:"label"`
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

// SkillDTO is one discovered skill for the config page.
type SkillDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Path        string `json:"path"`
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

// TurnStart identifies one started turn.
type TurnStart struct {
	RunID     string `json:"run_id"`
	ContextID string `json:"context_id"`
}

// TurnEnd reports a turn's terminal state.
type TurnEnd struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ReplyRequest is a user answer to one interaction.
type ReplyRequest struct {
	Text    string   `json:"text"`
	Option  *string  `json:"option"`
	Options []string `json:"options"`
	Cancel  bool     `json:"cancel"`
}

// InteractDTO is the rendered form of one runtime.Spec.
type InteractDTO struct {
	ID         string            `json:"id"`
	RunID      string            `json:"run_id"`
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Body       []json.RawMessage `json:"body"`
	Options    []OptionDTO       `json:"options"`
	Multi      bool              `json:"multi"`
	AllowOther bool              `json:"allow_other"`
	Source     string            `json:"source"`
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

func providerID(pid string) string { return strings.TrimSpace(pid) }
