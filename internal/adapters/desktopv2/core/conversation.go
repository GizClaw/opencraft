package core

import (
	"sync"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// Conversation owns the currently selected conversation and its UI
// defaults. Actual persistence lives in the sessions store on the
// active Host.
type Conversation struct {
	mu sync.Mutex

	currentID string
	mode      sessions.Mode
	think     string
	model     string
	runConvs  map[string]map[string]bool
}

// NewConversation creates the conversation service with workspace mode
// and medium reasoning defaults.
func NewConversation() *Conversation {
	return &Conversation{
		mode:     sessions.ModeWorkspace,
		think:    string(sessions.ThinkMedium),
		runConvs: make(map[string]map[string]bool),
	}
}

// New mints a fresh conversation id and resets UI defaults.
func (c *Conversation) New() string {
	id := sessions.NewID()
	c.mu.Lock()
	c.currentID = id
	c.mode = sessions.ModeWorkspace
	c.think = string(sessions.ThinkMedium)
	c.model = ""
	c.runConvs = make(map[string]map[string]bool)
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

// ForgetConversation drops run attribution when a conversation is
// deleted.
func (c *Conversation) ForgetConversation(conversationID string) {
	c.mu.Lock()
	delete(c.runConvs, conversationID)
	c.mu.Unlock()
}

// Current returns the active conversation id.
func (c *Conversation) Current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentID
}

// SetCurrent selects an existing conversation and its persisted
// settings.
func (c *Conversation) SetCurrent(
	id string, mode sessions.Mode, think, model string,
) {
	c.mu.Lock()
	c.currentID = id
	c.mode = mode
	c.think = think
	c.model = model
	c.mu.Unlock()
}

// Mode returns the current conversation sandbox mode.
func (c *Conversation) Mode() sessions.Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

// Think returns the current reasoning effort.
func (c *Conversation) Think() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.think
}

// Model returns the current model hint.
func (c *Conversation) Model() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.model
}

// SetMode updates the current conversation sandbox mode.
func (c *Conversation) SetMode(mode sessions.Mode) {
	c.mu.Lock()
	c.mode = mode
	c.mu.Unlock()
}

// SetThink updates the current reasoning effort.
func (c *Conversation) SetThink(think string) {
	c.mu.Lock()
	c.think = think
	c.mu.Unlock()
}

// SetModel updates the current model hint.
func (c *Conversation) SetModel(model string) {
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()
}
