package memory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/resource"
)

// TestFactoryWiresRouter verifies the memory assembly builds over a
// router dep (the wiring used by the embedded deploy document) and
// still assembles without one (buffer-fold-only deployments).
func TestFactoryWiresRouter(t *testing.T) {
	store, err := newMigratedSessions(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	router, err := route.New(
		&inference.Assembly{},
		route.Policy{
			Generate: []route.Pool{{
				Tier: "default",
				Targets: []route.Target{{Model: inference.ModelRef{
					ID: inference.ModelID{
						Provider: "deepseek",
						Name:     "deepseek-v4-flash",
					},
				}}},
			}},
		}.Selectors(&inference.Assembly{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	value, err := (Factory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"max_raw_messages":32,"preserve_recent":4}`),
		Deps: map[string]any{
			"sessions": store,
			"router":   router,
		},
	})
	if err != nil {
		t.Fatalf("factory with router: %v", err)
	}
	if _, ok := value.(corememory.TurnSink); !ok {
		t.Fatalf("value = %T, want corememory.TurnSink", value)
	}

	// Without a router the assembly still builds (buffer fold only).
	value, err = (Factory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"max_raw_messages":32,"preserve_recent":4}`),
		Deps: map[string]any{
			"sessions": store,
		},
	})
	if err != nil {
		t.Fatalf("factory without router: %v", err)
	}
	if _, ok := value.(corememory.TurnSink); !ok {
		t.Fatalf("value = %T, want corememory.TurnSink", value)
	}
}
