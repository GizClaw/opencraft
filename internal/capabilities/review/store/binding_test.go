package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestBindingEnabledRequiresQueueAndOptIn(t *testing.T) {
	store := newQueue(t)
	on := config.ReviewConfig{Enabled: true, EveryTurns: 5, MaxSuggestions: 3}
	off := config.ReviewConfig{Enabled: false, EveryTurns: 5, MaxSuggestions: 3}

	var nilBinding *Binding
	if nilBinding.Enabled() {
		t.Fatal("nil binding reported Enabled")
	}
	if (&Binding{Queue: Empty(), Config: on}).Enabled() {
		t.Fatal("empty queue reported Enabled")
	}
	if (&Binding{Queue: store, Config: off}).Enabled() {
		t.Fatal("disabled review reported Enabled")
	}
	if !(&Binding{Queue: store, Config: on}).Enabled() {
		t.Fatal("an open queue with the feature on must be Enabled")
	}
}

func TestFactorySpecDeclaresTheQueueContract(t *testing.T) {
	spec := (Factory{}).Spec()
	if spec.Kind != ResourceKind || spec.Impl != ResourceImpl {
		t.Fatalf("spec kind/impl = %s/%s", spec.Kind, spec.Impl)
	}
	if len(spec.Deps) != 1 {
		t.Fatalf("deps = %+v, want the single store dep", spec.Deps)
	}
	dep := spec.Deps[0]
	if dep.Name != "store" || dep.Type != reflect.TypeFor[Queue]() || !dep.Required {
		t.Fatalf("store dep = %+v, want a required StoreContract", dep)
	}
}

func TestFactoryBuildsBindingOverTheQueue(t *testing.T) {
	store := newQueue(t)
	value, err := (Factory{}).New(context.Background(), resource.NewInput([]byte(`{"enabled":true,"every_turns":3}`), map[string]any{"store": store}, nil, nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	binding := value
	if binding.Queue != store {
		t.Fatal("binding did not keep the injected queue")
	}
	if !binding.Config.Enabled || binding.Config.EveryTurns != 3 {
		t.Fatalf("config = %+v, want enabled with every_turns 3", binding.Config)
	}
}

func TestFactoryDefaultsReviewToOff(t *testing.T) {
	store := newQueue(t)
	value, err := (Factory{}).New(context.Background(), resource.NewInput([]byte(`{}`), map[string]any{"store": store}, nil, nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	binding := value
	if binding.Config.Enabled {
		t.Fatal("an empty settings subtree must leave the review off")
	}
	if binding.Config.EveryTurns != config.ReviewDefaultEveryTurns {
		t.Fatalf("every_turns = %d, want the default", binding.Config.EveryTurns)
	}
}

func TestFactoryRejectsMissingQueueAndBadSettings(t *testing.T) {
	ctx := context.Background()
	if _, err := (Factory{}).New(ctx, resource.NewInput([]byte(`{}`), nil, nil, nil)); err == nil {
		t.Fatal("New without a store dep must fail")
	}
	store := newQueue(t)
	if _, err := (Factory{}).New(ctx, resource.NewInput([]byte(`{}`), map[string]any{"store": "not a queue"}, nil, nil)); err == nil {
		t.Fatal("New with a mistyped store dep must fail")
	}
	if _, err := (Factory{}).New(ctx, resource.NewInput([]byte(`{"max_suggestions":99}`), map[string]any{"store": store}, nil, nil)); err == nil {
		t.Fatal("New with out-of-range settings must fail")
	}
}
