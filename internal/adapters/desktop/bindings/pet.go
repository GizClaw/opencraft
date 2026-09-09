package bindings

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
)

// Pet exposes the desktop pet pack registry and surface preferences to
// the UI. Packs are data (Rive state-machine specs) registered by the
// Cordis plugin host and consumed by pet windows; this binding is the
// cross-window bridge.
type Pet struct {
	core *core.Core
}

// NewPetBinding wires the pet binding.
func NewPetBinding(c *core.Core) *Pet {
	return &Pet{core: c}
}

// ListPacks returns every registered pet pack in registration order.
func (b *Pet) ListPacks() []pet.Pack {
	return b.core.Packs.List()
}

// RegisterPack adds or replaces one pet pack. Plugin packs override
// builtins with the same id; unregistering restores the previous pack.
func (b *Pet) RegisterPack(p pet.Pack) error {
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" || len(p.ID) > 64 {
		return fmt.Errorf("pet: invalid pack id %q", p.ID)
	}
	if p.StateMachine == "" {
		return fmt.Errorf("pet: pack %q requires a state machine", p.ID)
	}
	if len(p.Bindings) > 64 {
		return fmt.Errorf("pet: pack %q bindings exceed 64 entries", p.ID)
	}
	b.core.Packs.Register(p)
	b.core.Shell.Emit("pet:packs_changed", map[string]any{
		"pack_id": p.ID,
	})
	return nil
}

// UnregisterPack removes one plugin pack and restores the value it
// overrode (the builtin, when present).
func (b *Pet) UnregisterPack(id string) {
	b.core.Packs.Unregister(id)
	b.core.Shell.Emit("pet:packs_changed", map[string]any{
		"pack_id": id,
	})
}

// Activities returns the current per-agent pet activities. The main
// window Subagent Dock uses it to show what subagents are doing.
func (b *Pet) Activities() []pet.PetActivity {
	return b.core.Pet.Activities()
}

// MoveBy drags the pet window by a relative offset.
func (b *Pet) MoveBy(dx, dy int) {
	b.core.Shell.MovePetWindow(dx, dy)
}

// SetPosition moves the pet window to an absolute position.
func (b *Pet) SetPosition(x, y int) {
	b.core.Shell.SetPetPosition(x, y)
}

// Activate brings the main window to the foreground and focuses it.
func (b *Pet) Activate() {
	b.core.Shell.ActivatePet()
}

// Poke records a click/pet interaction with the pet.
func (b *Pet) Poke() {
	b.core.Shell.PokePet()
}

// SetRoamingPaused pauses or resumes the pet's autonomous roaming.
func (b *Pet) SetRoamingPaused(paused bool) {
	b.core.Shell.SetPetRoamingPaused(paused)
}

// Diagnostics returns the pet mind/rover snapshot for the settings
// diagnostics panel.
func (b *Pet) Diagnostics() pet.MindDebug {
	return b.core.Shell.PetDiagnostics()
}

// PositionDTO is the current pet window anchor exposed for drag
// gestures.
type PositionDTO struct {
	X     int  `json:"x"`
	Y     int  `json:"y"`
	Ready bool `json:"ready"`
}

// Position returns the rover-tracked window position (absolute DIP).
func (b *Pet) Position() PositionDTO {
	x, y, ok := b.core.Shell.PetPosition()
	return PositionDTO{X: x, Y: y, Ready: ok}
}

// PackAsset resolves a pack asset reference to base64 bytes for the
// Rive runtime. References use builtin://<id> or plugin://<pluginId>/<path>;
// builtin assets are embedded in the desktop binary.
func (b *Pet) PackAsset(asset string) (string, error) {
	if id, ok := strings.CutPrefix(asset, "builtin://"); ok {
		if id != pet.BuiltinAssistantPackID {
			return "", fmt.Errorf("pet: unknown builtin asset %q", asset)
		}
		return base64.StdEncoding.EncodeToString(pet.BuiltinAssistantPackAsset()), nil
	}
	pluginRef, ok := strings.CutPrefix(asset, "plugin://")
	if !ok {
		return "", fmt.Errorf("pet: invalid pack asset reference %q", asset)
	}
	pluginID, rel, found := strings.Cut(pluginRef, "/")
	if !found || pluginID == "" || rel == "" {
		return "", fmt.Errorf("pet: invalid plugin asset reference %q", asset)
	}
	data, err := b.core.Plugin.Store.Asset(pluginID, rel)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
