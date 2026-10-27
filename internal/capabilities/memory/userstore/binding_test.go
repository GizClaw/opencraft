package userstore

import (
	"context"
	"reflect"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestBindingEnabledRequiresStoreAndOptIn(t *testing.T) {
	store := newStore(t)
	on := config.UserMemoryConfig{Enabled: true, InjectMaxItems: 3, InjectMaxChars: 256}
	off := config.UserMemoryConfig{Enabled: false, InjectMaxItems: 3, InjectMaxChars: 256}

	// A nil binding (a runtime that declared no usermemory dep) is disabled.
	var nilBinding *Binding
	if nilBinding.Enabled() {
		t.Fatal("nil binding reported Enabled")
	}
	if (&Binding{Memory: Empty(), Config: on}).Enabled() {
		t.Fatal("empty store reported Enabled")
	}
	if (&Binding{Memory: store, Config: off}).Enabled() {
		t.Fatal("disabled config reported Enabled")
	}
	if !(&Binding{Memory: store, Config: on}).Enabled() {
		t.Fatal("an open store with the feature on must be Enabled")
	}
}

func TestFactorySpecDeclaresTheStoreContract(t *testing.T) {
	spec := (Factory{}).Spec()
	if spec.Kind != ResourceKind || spec.Impl != ResourceImpl {
		t.Fatalf("spec kind/impl = %s/%s", spec.Kind, spec.Impl)
	}
	if len(spec.Deps) != 1 {
		t.Fatalf("deps = %+v, want the single store dep", spec.Deps)
	}
	dep := spec.Deps[0]
	if dep.Name != "store" || dep.Type != reflect.TypeFor[Memory]() || !dep.Required {
		t.Fatalf("store dep = %+v, want a required StoreContract", dep)
	}
}

func TestFactoryBuildsBindingOverTheStore(t *testing.T) {
	store := newStore(t)
	value, err := (Factory{}).New(context.Background(), resource.NewInput([]byte(`{}`), map[string]any{"store": store}, nil, nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	binding := value
	if binding.Memory != store {
		t.Fatal("binding did not keep the injected store")
	}
	// An empty settings subtree resolves to the shipped defaults.
	if !binding.Config.Enabled ||
		binding.Config.InjectMaxItems != config.UserMemoryDefaultInjectMaxItems ||
		binding.Config.InjectMaxChars != config.UserMemoryDefaultInjectMaxChars {
		t.Fatalf("config = %+v, want the defaults", binding.Config)
	}
}

func TestFactoryRejectsMissingStoreAndBadSettings(t *testing.T) {
	ctx := context.Background()
	if _, err := (Factory{}).New(ctx, resource.NewInput([]byte(`{}`), nil, nil, nil)); err == nil {
		t.Fatal("New without a store dep must fail")
	}
	store := newStore(t)
	if _, err := (Factory{}).New(ctx, resource.NewInput([]byte(`{}`), map[string]any{"store": "not a memory"}, nil, nil)); err == nil {
		t.Fatal("New with a mistyped store dep must fail")
	}
	if _, err := (Factory{}).New(ctx, resource.NewInput([]byte(`{"inject_max_items":999}`), map[string]any{"store": store}, nil, nil)); err == nil {
		t.Fatal("New with out-of-range settings must fail")
	}
}
