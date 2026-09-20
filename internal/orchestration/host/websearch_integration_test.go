package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// searchTestInstance builds one enabled OpenAI deployment. A searchable
// instance declares hosted web search on its only model; the endpoint
// of a deployment behind a healthier router target is never contacted.
func searchTestInstance(stableID, endpoint, api string, searchable bool) config.Instance {
	in := config.Instance{
		StableID:  stableID,
		Type:      "openai",
		API:       api,
		Endpoint:  endpoint,
		Enabled:   true,
		KeySource: config.KeyLiteral,
		KeyValue:  "test-key",
		Models: []config.Model{{
			Name: "model-" + stableID,
			Kind: "generate",
		}},
	}
	if searchable {
		in.Models[0].Capabilities.HostedWebSearch = true
	}
	return in
}

// assertSearchBagProviders decodes the llm_extensions board var the
// assistant run was seeded with. The var holds the typed bag when the
// graph kept it as-is, so the assertion goes through JSON instead of a
// type assertion.
func assertSearchBagProviders(
	t *testing.T,
	res *agent.Result,
	want []string,
) {
	t.Helper()
	if res == nil || res.LastBoard == nil {
		t.Fatalf("run produced no board, want llm_extensions %v", want)
	}
	value, ok := res.LastBoard.GetVar("llm_extensions")
	if len(want) == 0 {
		if ok {
			t.Fatalf("llm_extensions = %#v, want unset", value)
		}
		return
	}
	if !ok {
		t.Fatalf("llm_extensions missing from the board, want %v", want)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode llm_extensions %s: %v", raw, err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Provider)
	}
	if len(got) != len(want) {
		t.Fatalf("llm_extensions providers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("llm_extensions providers = %v, want %v", got, want)
		}
	}
}

// TestHostSearchBagFollowsLiveRuntime pins the two halves of the hosted
// web search seed:
//
//   - an entry whose deployment the live runtime serves reaches the
//     board, so search keeps working for a healthy generation;
//   - an entry the live runtime cannot decode is dropped instead of
//     seeded. That is the deferred-rebuild window: the config file was
//     rewritten while the previous generation still serves turns, and
//     flowcraft rejects unregistered extension identities — seeding
//     one would fail the whole turn, not just disable search.
func TestHostSearchBagFollowsLiveRuntime(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Keep the sandbox local: the default (execd) needs a socket the
	// test environment does not provide.
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(filepath.Join(configDir, "opencraft.yaml"), seed, 0o600); err != nil {
		t.Fatal(err)
	}

	// The live generation serves the fake chat deployment (router
	// target one, so every turn lands there) plus a searchable
	// deployment that is registered but never called. Its decoder is
	// what makes the bag entry legal.
	live := config.InferenceConfig{Instances: []config.Instance{
		searchTestInstance("fake", provider.URL(), "chat", false),
		searchTestInstance("live-search", "https://example.invalid/v1",
			"responses", true),
	}}
	if err := configseed.Write(configDir, live); err != nil {
		t.Fatal(err)
	}

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "first"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	first, err := run.Wait(ctx)
	if err != nil || first.Err != nil {
		t.Fatalf("first run = %v err=%v", first, err)
	}
	assertSearchBagProviders(t, first, []string{"openai-live-search"})

	// The file moves ahead of the live generation: a second searchable
	// deployment the assembled runtime does not know. The turn must
	// survive with the drift entry dropped, keeping the entry the live
	// generation can decode.
	drifted := append(append([]config.Instance(nil), live.Instances...),
		searchTestInstance("drift-search", "https://example.invalid/v1",
			"responses", true))
	if err := configseed.Write(configDir,
		config.InferenceConfig{Instances: drifted}); err != nil {
		t.Fatal(err)
	}

	run2, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "second"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	second, err := run2.Wait(ctx)
	if err != nil || second.Err != nil {
		t.Fatalf("second run on a drifted config = %v err=%v", second, err)
	}
	assertSearchBagProviders(t, second, []string{"openai-live-search"})
	if calls := provider.Calls(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
}
