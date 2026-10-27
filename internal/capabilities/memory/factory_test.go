package memory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/inference/route"
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
	defer func() { _ = store.CloseDB() }()

	router, err := route.New(
		&inference.Assembly{},
		route.Policy{
			Generate: []route.Pool{{
				Tier: "default",
				Targets: []route.Target{{Model: model.ModelRef{
					ID: model.ModelID{
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

	_, err = (Factory{}).New(context.Background(), resource.NewInput([]byte(`{"max_raw_messages":32,"preserve_recent":4}`), map[string]any{
		"sessions": store,
		"router":   router,
	}, nil, nil))
	if err != nil {
		t.Fatalf("factory with router: %v", err)
	}

	// Without a router the assembly still builds (buffer fold only).
	_, err = (Factory{}).New(context.Background(), resource.NewInput([]byte(`{"max_raw_messages":32,"preserve_recent":4}`), map[string]any{
		"sessions": store,
	}, nil, nil))
	if err != nil {
		t.Fatalf("factory without router: %v", err)
	}
}

// TestFactoryReplayFullHistoryDefaultsOn pins the shipped policy: full
// history replay is the default, so a settings document that predates the
// switch (or a custom deployment that never wrote one) gets the same
// behaviour as the embedded document, and only an explicit false turns it
// back into the bounded raw window.
func TestFactoryReplayFullHistoryDefaultsOn(t *testing.T) {
	store, err := newMigratedSessions(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseDB() }()

	cases := []struct {
		name     string
		settings string
		want     bool
	}{
		{"absent", `{"max_raw_messages":32,"preserve_recent":4}`, true},
		{"true", `{"max_raw_messages":32,"replay_full_history":true}`, true},
		{"false", `{"max_raw_messages":32,"replay_full_history":false}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := (Factory{}).New(context.Background(), resource.NewInput([]byte(tc.settings), map[string]any{"sessions": store}, nil, nil))
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			assembly := value
			if got := assembly.ReplayFullHistory(); got != tc.want {
				t.Fatalf("ReplayFullHistory = %v, want %v", got, tc.want)
			}
		})
	}
}
