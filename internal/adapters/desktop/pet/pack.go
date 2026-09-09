package pet

import "sync"

// PackBinding maps one pet activity state (or tool category) to an
// input or trigger on the Rive state machine. "tool:*" is the wildcard
// fallback every character should provide.
type PackBinding struct {
	// Type is "input" for a boolean state input or "trigger" for a
	// one-shot trigger.
	Type string `json:"type"`
	Name string `json:"name"`
}

// PackMeta carries the render/window hints for one character.
type PackMeta struct {
	// Scale is the render scale relative to the 240px pet canvas.
	Scale float64 `json:"scale"`
	// WalkSpeed is the horizontal window speed in DIP/s used by the
	// director when the pet walks; the Rive walk animation should be
	// authored to match it.
	WalkSpeed float64 `json:"walkSpeed"`
	// Anchor pins the character to the window: "bottom-center" today.
	Anchor string `json:"anchor"`
}

// Pack is one declarative pet character. The .riv binary is delivered
// separately through the asset channel; RivAsset names its source
// (builtin://<id> or plugin://<pluginId>/<path>).
type Pack struct {
	ID           string                 `json:"id"`
	DisplayName  string                 `json:"displayName"`
	Version      string                 `json:"version"`
	PluginID     string                 `json:"pluginId,omitempty"`
	StateMachine string                 `json:"stateMachine"`
	RivAsset     string                 `json:"rivAsset,omitempty"`
	Meta         PackMeta               `json:"meta"`
	Bindings     map[string]PackBinding `json:"bindings"`
}

// BuiltinAssistantPackID is the canonical default assistant character.
const BuiltinAssistantPackID = "assistant-default"

// BuiltinAssistantPack returns the shipped default character. Its
// bindings mirror the renderer state vocabulary; the .riv asset is
// produced by the Rive pipeline and delivered as builtin://
// assistant-default. The intent bindings cover the one-shot reactions
// the Mind emits; welcome stays bubble-only so plugin characters are
// free to map it to their own greeting.
func BuiltinAssistantPack() Pack {
	return Pack{
		ID:           BuiltinAssistantPackID,
		DisplayName:  "OpenCraft Assistant",
		Version:      "0.2.0",
		StateMachine: "PetSM",
		RivAsset:     "builtin://assistant-default",
		Meta: PackMeta{
			Scale:     1,
			WalkSpeed: 90,
			Anchor:    "bottom-center",
		},
		Bindings: map[string]PackBinding{
			"idle":          {Type: "input", Name: "idle"},
			"walk":          {Type: "input", Name: "walk"},
			"thinking":      {Type: "trigger", Name: "think"},
			"tool:exec":     {Type: "input", Name: "exec"},
			"tool:file":     {Type: "input", Name: "file"},
			"tool:web":      {Type: "input", Name: "web"},
			"tool:generate": {Type: "input", Name: "generate"},
			"tool:*":        {Type: "input", Name: "busy"},
			"answering":     {Type: "input", Name: "talk"},
			"asking":        {Type: "trigger", Name: "ask"},
			"error":         {Type: "trigger", Name: "fail"},
			"done":          {Type: "trigger", Name: "cheer"},
			"intent:wave":    {Type: "trigger", Name: "wave"},
			"intent:look":    {Type: "trigger", Name: "look"},
			"intent:sulk":    {Type: "trigger", Name: "sulk"},
			"intent:zoomies": {Type: "trigger", Name: "zoomies"},
			"intent:nap":     {Type: "trigger", Name: "nap"},
		},
	}
}

// PackStore is the in-memory pet pack registry shared by the main
// window (plugin registration) and pet windows (rendering). User packs
// with the same id override builtins; unregistering restores the
// previous pack so plugin reloads never lose the builtin.
type PackStore struct {
	mu     sync.Mutex
	byID   map[string]Pack
	order  []string
	backup map[string]Pack
}

// NewPackStore seeds the registry with builtin packs.
func NewPackStore(builtins ...Pack) *PackStore {
	s := &PackStore{
		byID:   make(map[string]Pack),
		backup: make(map[string]Pack),
	}
	for _, p := range builtins {
		s.Register(p)
	}
	return s
}

// Register adds or replaces one pack. Replacing an existing pack keeps
// the previous value so Unregister can restore it.
func (s *PackStore) Register(p Pack) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.byID[p.ID]
	if _, backed := s.backup[p.ID]; existed && !backed {
		s.backup[p.ID] = s.byID[p.ID]
	}
	if !existed {
		s.order = append(s.order, p.ID)
	}
	s.byID[p.ID] = p
	return existed
}

// Unregister removes a pack and restores the pre-override value when
// one exists.
func (s *PackStore) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.backup[id]; ok {
		s.byID[id] = prev
		delete(s.backup, id)
		return
	}
	if _, ok := s.byID[id]; !ok {
		return
	}
	delete(s.byID, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// List returns packs in registration order.
func (s *PackStore) List() []Pack {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pack, 0, len(s.order))
	for _, id := range s.order {
		if p, ok := s.byID[id]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Get returns one pack by id.
func (s *PackStore) Get(id string) (Pack, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	return p, ok
}
