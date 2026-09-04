package core

import (
	"strings"
	"sync"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Conversation owns each workspace's currently selected conversation
// and its UI defaults. Actual persistence lives in the sessions store
// on the workspace's Host.
type Conversation struct {
	mu sync.Mutex

	byWID    map[string]*workspaceConv
	runConvs map[string]map[string]bool
}

type workspaceConv struct {
	currentID string
	mode      sessions.Mode
	think     string
	model     string
}

// NewConversation creates the conversation service with workspace mode
// and medium reasoning defaults.
func NewConversation() *Conversation {
	return &Conversation{
		byWID:    make(map[string]*workspaceConv),
		runConvs: make(map[string]map[string]bool),
	}
}

// workspaceKey canonicalizes the UI's active path to the same stable
// workspace id every other subsystem uses for state and storage.
func workspaceKey(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	return config.WorkspaceID(workDir)
}

func (c *Conversation) stateLocked(workDir string) *workspaceConv {
	id := workspaceKey(workDir)
	st := c.byWID[id]
	if st == nil {
		st = &workspaceConv{
			mode:  sessions.ModeWorkspace,
			think: string(sessions.ThinkMedium),
		}
		c.byWID[id] = st
	}
	return st
}

// New mints a fresh conversation id for one workspace and resets that
// workspace's UI defaults. Conversations and runs already minted for
// other workspaces stay untouched.
func (c *Conversation) New(workDir string) string {
	id := sessions.NewID()
	c.mu.Lock()
	st := c.stateLocked(workDir)
	st.currentID = id
	st.mode = sessions.ModeWorkspace
	st.think = string(sessions.ThinkMedium)
	st.model = ""
	c.mu.Unlock()
	return id
}

// TrackRun remembers one run id minted per conversation.
func (c *Conversation) TrackRun(conversationID, runID string) {
	c.mu.Lock()
	if c.runConvs[conversationID] == nil {
		c.runConvs[conversationID] = make(map[string]bool)
	}
	c.runConvs[conversationID][runID] = true
	c.mu.Unlock()
}

// Runs returns the run ids attributed to one conversation.
func (c *Conversation) Runs(conversationID string) map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]bool)
	for id := range c.runConvs[conversationID] {
		out[id] = true
	}
	return out
}

// ConversationForRun returns the conversation that minted runID, or
// "" when the run is not attributed to any conversation.
func (c *Conversation) ConversationForRun(runID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for conversationID, runs := range c.runConvs {
		if runs[runID] {
			return conversationID
		}
	}
	return ""
}

// ForgetConversation drops run attribution when a conversation is
// deleted.
func (c *Conversation) ForgetConversation(conversationID string) {
	c.mu.Lock()
	delete(c.runConvs, conversationID)
	c.mu.Unlock()
}

// Current returns the active conversation id for one workspace.
func (c *Conversation) Current(workDir string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.byWID[workspaceKey(workDir)]; st != nil {
		return st.currentID
	}
	return ""
}

// SetCurrent selects an existing conversation and its persisted
// settings.
func (c *Conversation) SetCurrent(
	workDir, id string, mode sessions.Mode, think, model string,
) {
	c.mu.Lock()
	st := c.stateLocked(workDir)
	st.currentID = id
	st.mode = mode
	st.think = think
	st.model = model
	c.mu.Unlock()
}

// Mode returns the current conversation sandbox mode for one
// workspace.
func (c *Conversation) Mode(workDir string) sessions.Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.byWID[workspaceKey(workDir)]; st != nil {
		return st.mode
	}
	return sessions.ModeWorkspace
}

// Think returns the current reasoning effort for one workspace.
func (c *Conversation) Think(workDir string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.byWID[workspaceKey(workDir)]; st != nil {
		return st.think
	}
	return string(sessions.ThinkMedium)
}

// Model returns the current model hint for one workspace.
func (c *Conversation) Model(workDir string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.byWID[workspaceKey(workDir)]; st != nil {
		return st.model
	}
	return ""
}

// SetMode updates one workspace's current conversation sandbox mode.
func (c *Conversation) SetMode(workDir string, mode sessions.Mode) {
	c.mu.Lock()
	c.stateLocked(workDir).mode = mode
	c.mu.Unlock()
}

// SetThink updates one workspace's current reasoning effort.
func (c *Conversation) SetThink(workDir, think string) {
	c.mu.Lock()
	c.stateLocked(workDir).think = think
	c.mu.Unlock()
}

// SetModel updates one workspace's current model hint.
func (c *Conversation) SetModel(workDir, model string) {
	c.mu.Lock()
	c.stateLocked(workDir).model = model
	c.mu.Unlock()
}
