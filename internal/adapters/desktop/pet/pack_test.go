package pet

import (
	"encoding/json"
	"strings"
	"testing"
)

// samplePack returns a minimal valid contract v2 pack.
func samplePack(id string) Pack {
	return Pack{
		ID:           id,
		DisplayName:  id,
		Version:      "1.0.0",
		PluginID:     "test-plugin",
		Artboard:     "Pet",
		StateMachine: "PetSM",
		ViewModel:    "PetVM",
		RivAsset:     "plugin://test-plugin/" + id + ".riv",
		Meta:         PackMeta{Scale: 1, WalkSpeed: 110},
		Bindings: map[string]PackBinding{
			"phase":   {Type: PackBindingString, Property: "phase"},
			"walking": {Type: PackBindingBoolean, Property: "walking"},
		},
	}
}

func TestPackStoreRegisterOverrideRestore(t *testing.T) {
	store := NewPackStore(BuiltinAssistantPack())
	user := samplePack(BuiltinAssistantPackID)
	user.DisplayName = "User cat"

	if replaced := store.Register(user); !replaced {
		t.Fatal("overriding a builtin must report replacement")
	}
	got, ok := store.Get(BuiltinAssistantPackID)
	if !ok || got.DisplayName != "User cat" {
		t.Fatalf("override not visible: %+v", got)
	}

	store.Unregister(BuiltinAssistantPackID)
	got, ok = store.Get(BuiltinAssistantPackID)
	if !ok || got.DisplayName != BuiltinAssistantPack().DisplayName {
		t.Fatalf("builtin not restored after unregister: %+v", got)
	}
}

func TestPackStoreListAndRemove(t *testing.T) {
	store := NewPackStore(BuiltinAssistantPack())
	store.Register(samplePack("custom"))
	list := store.List()
	if len(list) != 2 || list[0].ID != BuiltinAssistantPackID || list[1].ID != "custom" {
		t.Fatalf("list order = %+v", list)
	}
	store.Unregister("custom")
	if list = store.List(); len(list) != 1 {
		t.Fatalf("custom pack not removed: %+v", list)
	}
}

func TestPackValidate(t *testing.T) {
	mutate := func(edit func(p *Pack)) Pack {
		p := samplePack("sample")
		edit(&p)
		return p
	}

	cases := []struct {
		name    string
		pack    Pack
		wantErr string
	}{
		{name: "valid", pack: samplePack("sample")},
		{
			name:    "missing id",
			pack:    mutate(func(p *Pack) { p.ID = "" }),
			wantErr: "invalid pack id",
		},
		{
			name:    "overlong id",
			pack:    mutate(func(p *Pack) { p.ID = strings.Repeat("x", 65) }),
			wantErr: "invalid pack id",
		},
		{
			name:    "missing artboard",
			pack:    mutate(func(p *Pack) { p.Artboard = "  " }),
			wantErr: "requires an artboard",
		},
		{
			name:    "non-positive walk speed",
			pack:    mutate(func(p *Pack) { p.Meta.WalkSpeed = 0 }),
			wantErr: "walkSpeed must be in",
		},
		{
			name:    "walk speed above cap",
			pack:    mutate(func(p *Pack) { p.Meta.WalkSpeed = maxPackWalkSpeed + 1 }),
			wantErr: "walkSpeed must be in",
		},
		{
			name:    "non-positive scale",
			pack:    mutate(func(p *Pack) { p.Meta.Scale = 0 }),
			wantErr: "scale must be in",
		},
		{
			name:    "scale above cap",
			pack:    mutate(func(p *Pack) { p.Meta.Scale = maxPackScale + 1 }),
			wantErr: "scale must be in",
		},
		{
			name: "too many bindings",
			pack: mutate(func(p *Pack) {
				for i := 0; i <= maxPackBindings; i++ {
					p.Bindings[string(rune('a'+i%26))+string(rune('a'+i/26))] = PackBinding{
						Type:     PackBindingBoolean,
						Property: "flag",
					}
				}
			}),
			wantErr: "bindings exceed",
		},
		{
			name: "unknown slot",
			pack: mutate(func(p *Pack) {
				p.Bindings["mood"] = PackBinding{
					Type:     PackBindingBoolean,
					Property: "mood",
				}
			}),
			wantErr: "unknown binding slot",
		},
		{
			name: "slot that merely resembles facing",
			pack: mutate(func(p *Pack) {
				p.Bindings["facings"] = PackBinding{
					Type:     PackBindingString,
					Property: "facing",
				}
			}),
			wantErr: "unknown binding slot",
		},
		{
			name: "empty intent slot",
			pack: mutate(func(p *Pack) {
				p.Bindings["intent:"] = PackBinding{
					Type:     PackBindingTrigger,
					Property: "wave",
				}
			}),
			wantErr: "unknown binding slot",
		},
		{
			name: "missing property",
			pack: mutate(func(p *Pack) {
				p.Bindings["phase"] = PackBinding{Type: PackBindingString}
			}),
			wantErr: "requires a property",
		},
		{
			name: "enum without values",
			pack: mutate(func(p *Pack) {
				p.Bindings["phase"] = PackBinding{Type: PackBindingEnum, Property: "phase"}
			}),
			wantErr: "needs values",
		},
		{
			name: "boolean with values",
			pack: mutate(func(p *Pack) {
				p.Bindings["walking"] = PackBinding{
					Type:     PackBindingBoolean,
					Property: "walking",
					Values:   map[string]string{"true": "Walk"},
				}
			}),
			wantErr: "cannot map values",
		},
		{
			name: "trigger with fallback",
			pack: mutate(func(p *Pack) {
				p.Bindings["intent:wave"] = PackBinding{
					Type:     PackBindingTrigger,
					Property: "wave",
					Fallback: "Wave",
				}
			}),
			wantErr: "cannot map values",
		},
		{
			name: "invalid type",
			pack: mutate(func(p *Pack) {
				p.Bindings["phase"] = PackBinding{Type: "input", Property: "idle"}
			}),
			wantErr: "invalid type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.pack.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestPackValidateAcceptsMappedTypes covers the value-mapping types the
// renderer relies on: enum/string may translate activity values, the
// remaining types are written as-is.
func TestPackValidateAcceptsMappedTypes(t *testing.T) {
	cases := []struct {
		name    string
		slot    string
		binding PackBinding
	}{
		{
			name: "enum with fallback",
			slot: "tool",
			binding: PackBinding{
				Type:     PackBindingEnum,
				Property: "tool",
				Values:   map[string]string{"file": "File"},
				Fallback: "Busy",
			},
		},
		{
			name: "string with fallback",
			slot: "phase",
			binding: PackBinding{
				Type:     PackBindingString,
				Property: "phase",
				Values:   map[string]string{"idle": "Idle"},
				Fallback: "Idle",
			},
		},
		{name: "string without values", slot: "phase", binding: PackBinding{
			Type: PackBindingString, Property: "phase"}},
		{name: "number", slot: "walking", binding: PackBinding{
			Type: PackBindingNumber, Property: "speed"}},
		{name: "color", slot: "walking", binding: PackBinding{
			Type: PackBindingColor, Property: "tint"}},
		{name: "boolean", slot: "sleeping", binding: PackBinding{
			Type: PackBindingBoolean, Property: "sleeping"}},
		{name: "trigger intent", slot: "intent:zoomies", binding: PackBinding{
			Type: PackBindingTrigger, Property: "zoomies"}},
		{name: "facing string", slot: "facing", binding: PackBinding{
			Type: PackBindingString, Property: "facing"}},
		{name: "facing enum with values", slot: "facing", binding: PackBinding{
			Type:     PackBindingEnum,
			Property: "facing",
			Values:   map[string]string{"left": "TurnLeft", "right": "TurnRight"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := samplePack("sample")
			p.Bindings = map[string]PackBinding{tc.slot: tc.binding}
			if err := p.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestBuiltinAssistantPackIsValid keeps the panic in
// BuiltinAssistantPack (a malformed embedded definition) unreachable and
// pins the fields the renderer reads by name.
func TestBuiltinAssistantPackIsValid(t *testing.T) {
	pack := BuiltinAssistantPack()
	if err := pack.Validate(); err != nil {
		t.Fatalf("builtin pack is invalid: %v", err)
	}
	if pack.ID != BuiltinAssistantPackID {
		t.Fatalf("builtin pack id = %q", pack.ID)
	}
	if pack.Version != "1.0.0" {
		t.Fatalf("builtin pack version = %q, want 1.0.0", pack.Version)
	}
	if pack.Artboard != "Pet" || pack.StateMachine != "PetSM" ||
		pack.ViewModel != "PetVM" {
		t.Fatalf("builtin pack names = %q/%q/%q, want Pet/PetSM/PetVM",
			pack.Artboard, pack.StateMachine, pack.ViewModel)
	}
	if pack.RivAsset != "builtin://"+BuiltinAssistantPackID {
		t.Fatalf("builtin pack asset = %q", pack.RivAsset)
	}
}

// TestBuiltinAssistantPackCoversActivityVocabulary ties the shipped pack
// to the Go vocabulary: every phase the director can report and every
// tool category the asset draws must resolve to a property value, and
// the reactive slots must be bound as the renderer expects.
func TestBuiltinAssistantPackCoversActivityVocabulary(t *testing.T) {
	pack := BuiltinAssistantPack()

	phase, ok := pack.Bindings["phase"]
	if !ok {
		t.Fatal("builtin pack has no phase binding")
	}
	for _, phaseValue := range []PetPhase{
		PetPhaseIdle, PetPhaseThinking, PetPhaseTool, PetPhaseAnswering,
		PetPhaseAsking, PetPhaseDone, PetPhaseError,
	} {
		if phase.Values[string(phaseValue)] == "" {
			t.Errorf("builtin pack maps no value for phase %q", phaseValue)
		}
	}

	tool, ok := pack.Bindings["tool"]
	if !ok {
		t.Fatal("builtin pack has no tool binding")
	}
	if tool.Fallback == "" {
		t.Error("builtin pack tool binding needs a fallback for unknown categories")
	}
	for _, category := range []PetToolCategory{
		PetToolCategoryExec, PetToolCategoryFile, PetToolCategoryWeb,
		PetToolCategoryGenerate,
	} {
		if tool.Values[string(category)] == "" {
			t.Errorf("builtin pack maps no value for tool category %q", category)
		}
	}

	for slot, want := range map[string]PackBindingType{
		"walking":        PackBindingBoolean,
		"sleeping":       PackBindingBoolean,
		"intent:wave":    PackBindingTrigger,
		"intent:look":    PackBindingTrigger,
		"intent:sulk":    PackBindingTrigger,
		"intent:zoomies": PackBindingTrigger,
	} {
		binding, ok := pack.Bindings[slot]
		if !ok {
			t.Errorf("builtin pack has no %q binding", slot)
			continue
		}
		if binding.Type != want {
			t.Errorf("%q binding type = %q, want %q", slot, binding.Type, want)
		}
	}

	// welcome greets through the bubble and nap is expressed by the
	// sleeping flag, so neither may be bound to a trigger.
	for _, slot := range []string{"intent:welcome", "intent:nap"} {
		if _, ok := pack.Bindings[slot]; ok {
			t.Errorf("builtin pack must not bind %q", slot)
		}
	}
}

// TestBuiltinAssistantPackIsCopied guards the embedded definition
// against callers mutating shared state through the returned pack.
func TestBuiltinAssistantPackIsCopied(t *testing.T) {
	first := BuiltinAssistantPack()
	first.Bindings["phase"] = PackBinding{Type: PackBindingTrigger, Property: "boom"}
	first.Bindings["intent:wave"] = PackBinding{
		Type:   PackBindingTrigger,
		Values: map[string]string{"x": "y"},
	}

	second := BuiltinAssistantPack()
	if binding := second.Bindings["phase"]; binding.Property == "boom" {
		t.Fatal("bindings map is shared with the embedded definition")
	}
	if _, ok := second.Bindings["intent:wave"].Values["x"]; ok {
		t.Fatal("binding values map is shared with the embedded definition")
	}
}

// TestBuiltinAssistantPackJSONIsStrict pins the embedded definition to
// the contract structs: an unknown key would otherwise be silently
// dropped and the pack would render a character that ignores it.
func TestBuiltinAssistantPackJSONIsStrict(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(string(builtinAssistantPackJSON)))
	decoder.DisallowUnknownFields()
	var pack Pack
	if err := decoder.Decode(&pack); err != nil {
		t.Fatalf("embedded pack does not match the contract: %v", err)
	}
}
