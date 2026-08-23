package desktop

import (
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/agents"
	"github.com/GizClaw/opencraft/internal/setup"
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

// ProviderView is one entry of the onboarding provider catalog.
type ProviderView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultModel string `json:"default_model"`
	EnvVar       string `json:"env_var"`
	API          string `json:"api"`
	Azure        bool   `json:"azure"`
}

// SetupProvider is one provider selection from the frontend, in
// router priority order.
type SetupProvider struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	KeyEnv    bool   `json:"key_env"`
	Model     string `json:"model"`
	Endpoint  string `json:"endpoint"`
	Vision    bool   `json:"vision"`
	Reasoning string `json:"reasoning"`
	WebSearch bool   `json:"web_search"`
}

// SetupRequest is the full onboarding payload.
type SetupRequest struct {
	Providers []SetupProvider `json:"providers"`
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
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Body       []json.RawMessage `json:"body"`
	Options    []OptionDTO     `json:"options"`
	Multi      bool            `json:"multi"`
	AllowOther bool            `json:"allow_other"`
	Source     string          `json:"source"`
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
	Model           string `json:"model"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	LatencyMs       int64  `json:"latency_ms"`
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
func providerByID(id string) (setup.Provider, bool) {
	for _, p := range setup.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return setup.Provider{}, false
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
