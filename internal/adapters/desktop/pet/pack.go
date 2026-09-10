package pet

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// PackBindingType is the Rive view model property type a binding
// targets. Every type except trigger is a value property: writing a new
// value is how the renderer changes state, and mutual exclusion comes
// from the value itself rather than from releasing inputs.
type PackBindingType string

const (
	PackBindingEnum    PackBindingType = "enum"
	PackBindingString  PackBindingType = "string"
	PackBindingBoolean PackBindingType = "boolean"
	PackBindingNumber  PackBindingType = "number"
	PackBindingColor   PackBindingType = "color"
	PackBindingTrigger PackBindingType = "trigger"
)

// PackBinding maps one pet activity slot to a view model property.
//
// Slots are "phase", "tool", "walking", "sleeping" and "intent:<name>".
// For enum/string slots Values translates the activity value into the
// asset's vocabulary; Fallback is written when the activity carries a
// value the table does not cover (an unknown tool category, say).
type PackBinding struct {
	Type PackBindingType `json:"type"`
	// Property is the view model property name.
	Property string `json:"property"`
	// Values maps activity values (phase names, tool categories) to
	// property values. Only meaningful for enum and string bindings.
	Values map[string]string `json:"values,omitempty"`
	// Fallback is the property value written for activity values the
	// table does not cover. Empty means "leave the property alone".
	Fallback string `json:"fallback,omitempty"`
}

// PackMeta carries the render/window hints for one character.
type PackMeta struct {
	// Scale is the render scale relative to the 240px pet canvas.
	Scale float64 `json:"scale"`
	// WalkSpeed is the horizontal window speed in DIP/s the director
	// moves the pet at when walking; the Rive walk animation is
	// authored to match it: one hop per 55 DIP of travel (2 hops/s at
	// 110 DIP/s).
	WalkSpeed float64 `json:"walkSpeed"`
}

// Pack is one declarative pet character driving one Rive artboard
// through its view model. The .riv binary is delivered separately
// through the asset channel; RivAsset names its source
// (builtin://<id> or plugin://<pluginId>/<path>).
type Pack struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Version     string `json:"version"`
	PluginID    string `json:"pluginId,omitempty"`
	// Artboard is the required artboard name inside the .riv.
	Artboard string `json:"artboard"`
	// StateMachine is optional: without it the character only reacts
	// to data binding and plays no state machine.
	StateMachine string `json:"stateMachine,omitempty"`
	// ViewModel is optional: empty uses the artboard's default view
	// model instance.
	ViewModel string                 `json:"viewModel,omitempty"`
	RivAsset  string                 `json:"rivAsset,omitempty"`
	Meta      PackMeta               `json:"meta"`
	Bindings  map[string]PackBinding `json:"bindings"`
}

// maxPackBindings caps how many bindings one pack may declare.
const maxPackBindings = 64

// maxPackIDLen caps pack ids so they stay usable as registry keys.
const maxPackIDLen = 64

// Validate checks the declarative pack contract. It is the single gate
// shared by builtin packs, plugin packs and tests; asset-side checks
// (does the artboard/view model/property exist?) run in the renderer,
// which is the only place that has the .riv loaded.
func (p Pack) Validate() error {
	if p.ID == "" || len(p.ID) > maxPackIDLen {
		return fmt.Errorf("pet: invalid pack id %q", p.ID)
	}
	if strings.TrimSpace(p.Artboard) == "" {
		return fmt.Errorf("pet: pack %q requires an artboard", p.ID)
	}
	if len(p.Bindings) > maxPackBindings {
		return fmt.Errorf("pet: pack %q bindings exceed %d entries",
			p.ID, maxPackBindings)
	}
	if p.Meta.WalkSpeed <= 0 || p.Meta.WalkSpeed > maxPackWalkSpeed {
		return fmt.Errorf("pet: pack %q walkSpeed must be in (0, %d]",
			p.ID, maxPackWalkSpeed)
	}
	if p.Meta.Scale <= 0 || p.Meta.Scale > maxPackScale {
		return fmt.Errorf("pet: pack %q scale must be in (0, %d]",
			p.ID, maxPackScale)
	}
	for slot, binding := range p.Bindings {
		if err := binding.validate(p.ID, slot); err != nil {
			return err
		}
	}
	return nil
}

// maxPackScale caps the render scale so a pack cannot ask for a canvas
// size the pet window is not built for.
const maxPackScale = 4

// maxPackWalkSpeed caps the walking window speed in DIP/s. The rover
// re-anchors on the OS-reported window position every few ticks and
// treats a larger delta as a drag, so a pack that moved faster than
// this would be fighting the anchor instead of walking.
const maxPackWalkSpeed = 1200

func (b PackBinding) validate(packID, slot string) error {
	known := slot == "phase" || slot == "tool" ||
		slot == "walking" || slot == "sleeping" ||
		(strings.HasPrefix(slot, "intent:") && len(slot) > len("intent:"))
	if !known {
		return fmt.Errorf("pet: pack %q has unknown binding slot %q",
			packID, slot)
	}
	if strings.TrimSpace(b.Property) == "" {
		return fmt.Errorf("pet: pack %q binding %q requires a property",
			packID, slot)
	}
	switch b.Type {
	case PackBindingEnum:
		if len(b.Values) == 0 {
			return fmt.Errorf("pet: pack %q binding %q needs values",
				packID, slot)
		}
	case PackBindingString:
		// Values are optional: a string property may be fed straight
		// from the activity value (see the renderer's write rules).
	case PackBindingBoolean, PackBindingNumber, PackBindingColor,
		PackBindingTrigger:
		if len(b.Values) > 0 || b.Fallback != "" {
			return fmt.Errorf(
				"pet: pack %q binding %q cannot map values for type %q",
				packID, slot, b.Type)
		}
	default:
		return fmt.Errorf("pet: pack %q binding %q has invalid type %q",
			packID, slot, b.Type)
	}
	return nil
}

// BuiltinAssistantPackID is the canonical default assistant character.
const BuiltinAssistantPackID = "assistant-default"

// builtinAssistantPackJSON is the shipped pack definition, kept next to
// the .riv it describes. It is the single source of truth shared with
// the renderer: the picture and the driver cannot drift because both
// sides read this file (the frontend asset contract test reads it too)
// and the asset is validated against it on mount.
//
// The intent bindings cover the one-shot reactions the Mind emits as
// triggers. Two intents stay unmapped on purpose: welcome is
// bubble-only so plugin characters can greet their own way, and nap is
// expressed by the sleeping flag (the mind's nap intent only marks the
// moment it fell asleep).
//
// phase and tool bind string properties rather than enums: the asset
// carries no DataEnum, and the contract allows string as the
// equivalent route.
//
//go:embed assets/assistant-default.pack.json
var builtinAssistantPackJSON []byte

var (
	builtinPackOnce sync.Once
	builtinPack     Pack
	builtinPackErr  error
)

// BuiltinAssistantPack returns the shipped default character. The
// definition is embedded, so pack-level mistakes (a typo'd key, a
// missing artboard) can only be fixed in the source; they panic here
// instead of degrading into a pet that renders nothing, and
// TestBuiltinAssistantPackIsValid keeps that panic unreachable.
//
// The returned pack is a fresh copy: callers may keep it or hand it to
// the registry without aliasing the embedded data.
func BuiltinAssistantPack() Pack {
	builtinPackOnce.Do(func() {
		decoder := json.NewDecoder(bytes.NewReader(builtinAssistantPackJSON))
		decoder.DisallowUnknownFields()
		builtinPackErr = decoder.Decode(&builtinPack)
		if builtinPackErr == nil {
			builtinPackErr = builtinPack.Validate()
		}
	})
	if builtinPackErr != nil {
		panic(fmt.Sprintf("pet: builtin pack is malformed: %v",
			builtinPackErr))
	}
	return clonePack(builtinPack)
}

// clonePack deep-copies the maps a Pack owns so the embedded builtin
// definition never becomes shared mutable state.
func clonePack(p Pack) Pack {
	out := p
	out.Bindings = make(map[string]PackBinding, len(p.Bindings))
	for slot, binding := range p.Bindings {
		cloned := binding
		if binding.Values != nil {
			cloned.Values = make(map[string]string, len(binding.Values))
			for key, value := range binding.Values {
				cloned.Values[key] = value
			}
		}
		out.Bindings[slot] = cloned
	}
	return out
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
