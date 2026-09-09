package pet

import "testing"

func samplePack(id string) Pack {
	return Pack{
		ID:           id,
		DisplayName:  id,
		Version:      "1.0.0",
		PluginID:     "test-plugin",
		StateMachine: "PetSM",
		RivAsset:     "plugin://test-plugin/" + id + ".riv",
		Meta:         PackMeta{Scale: 1, WalkSpeed: 90, Anchor: "bottom-center"},
		Bindings: map[string]PackBinding{
			"idle": {Type: "input", Name: "idle"},
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
