package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
	"github.com/GizClaw/opencraft/internal/testing/kraftfixture"
)

// TestPluginInstalledMidTurnIsCallableInThatTurn is the live-run half of
// the clock's agent row: the agent's own plugin_install lands while the
// turn that asked for it is still running, and the runtime swap is
// deferred to the drain (the install must not tear down its own run).
// The plugin therefore has to reach the turn through the other path —
// the tool source republishes on the registry's signal, and Definitions()
// is read once per round — which is exactly what this test would lose if
// the source went back to a sync.Once snapshot or republished only at
// assembly: the model's call would land on a name the registry does not
// have, and the round would fail with an unknown tool.
func TestPluginInstalledMidTurnIsCallableInThatTurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the mid-turn plugin install test in short mode")
	}
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{
			// Round 1 runs a tool the assembly already had, which is
			// what gives the turn a second round.
			{Name: "exec_command", Arguments: `{"command": "true"}`},
		}},
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{
			// Round 2 calls the tool the plugin installed *during*
			// round 1 brought in. It is deferred like every plugin
			// tool, so the model has to find it first — and the search
			// is itself the assertion: an unrepublished tool is not in
			// the catalog to be found.
			{Name: "tool_search", Arguments: `{"query": "plugin kraft pid"}`},
		}},
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{
			{Name: "plug__pid", Arguments: "{}"},
		}},
		fakeprovider.Reply{Text: "done"},
	)
	workDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, t.TempDir(), "")
	ctx := context.Background()
	c.SetWorkDir(workDir)
	if err := c.RebuildRuntime(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	first := c.ActiveHost()
	if first == nil {
		t.Fatal("no current host after rebuild")
	}
	t.Cleanup(func() {
		// The install armed a replacement for the runtime the turn was
		// holding. Let it land first, then close it and the pool: a
		// replacement that arrives after this cleanup reopens the
		// workspace store and races the temp dirs' removal.
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			h := c.ActiveHost()
			if h != nil && h != first && !h.IsStale() {
				_ = h.Close()
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		c.Runtime.Close()
		c.Plugin.Close()
	})

	gate := provider.HoldNext()
	run, err := first.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "install while I wait"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer gate.Release()
	select {
	case <-gate.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("run did not reach the provider")
	}

	// The install lands while the turn is live.
	binary := kraftfixture.Build(t, kraftfixture.SecretPlugin)
	src := filepath.Join(workDir, ".opencraft-plugins", "plug")
	fixturePluginSource(t, src, binary, "plug", "0.1.0")
	if _, err := NewPluginInstaller(c).PluginInstall(ctx, src); err != nil {
		t.Fatalf("mid-turn install: %v", err)
	}
	// The swap waited for the drain: the turn keeps the Host it started
	// on, which is why the republish path has to carry the change.
	if got := c.ActiveHost(); got != first {
		t.Fatal("the mid-turn install replaced the live Host instead of " +
			"deferring the swap to the drain")
	}
	gate.Release()

	res, err := run.Wait(ctx)
	if err != nil || res == nil || res.Status != "completed" {
		t.Fatalf("run wait = %+v, %v; want completed", res, err)
	}
	// The search found it: the catalog the round reads is the live
	// registry, and an unrepublished tool would not be in it.
	if !searchHit(t, provider, "plug__pid") {
		t.Fatalf("tool_search never saw the plugin tool; messages=%s",
			dumpMessages(t, provider))
	}
	// ...and the call ran: the fixture's own pid came back as the tool
	// result of the round that followed.
	if pid := fixturePidResult(t, provider); pid <= 0 {
		t.Fatalf("no tool result carried the fixture's pid: %s",
			dumpMessages(t, provider))
	}
}

// searchHit reports whether a tool_search result named one tool.
func searchHit(t *testing.T, provider *fakeprovider.Server, tool string) bool {
	t.Helper()
	for _, messages := range messagesOf(t, provider) {
		for _, m := range messages {
			if role, _ := m["role"].(string); role != "tool" {
				continue
			}
			if strings.Contains(contentOf(m), `"`+tool+`"`) {
				return true
			}
		}
	}
	return false
}

// fixturePidResult returns the pid a plugin tool result reported. A tool
// message whose text decodes as {"pid": N} is the fixture's own answer;
// everything else (search results, other tools) does not decode.
func fixturePidResult(t *testing.T, provider *fakeprovider.Server) int {
	t.Helper()
	for _, messages := range messagesOf(t, provider) {
		for _, m := range messages {
			if role, _ := m["role"].(string); role != "tool" {
				continue
			}
			var out struct {
				PID int `json:"pid"`
			}
			if err := json.Unmarshal([]byte(contentOf(m)), &out); err == nil &&
				out.PID > 0 {
				return out.PID
			}
		}
	}
	return 0
}

// messagesOf reads what reached the provider, oldest request first.
func messagesOf(
	t *testing.T, provider *fakeprovider.Server,
) [][]map[string]any {
	t.Helper()
	all, err := provider.MessagesForCalls()
	if err != nil {
		t.Fatalf("read provider messages: %v", err)
	}
	return all
}

// contentOf returns the text content of one message, empty when it has
// none.
func contentOf(m map[string]any) string {
	switch v := m["content"].(type) {
	case string:
		return v
	case []any:
		for _, part := range v {
			obj, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := obj["text"].(string); ok {
				return text
			}
		}
	}
	return ""
}

// dumpMessages renders what reached the provider, for a failure that
// needs to show whether the tool result came back at all.
func dumpMessages(t *testing.T, provider *fakeprovider.Server) string {
	t.Helper()
	raw, err := json.Marshal(messagesOf(t, provider))
	if err != nil {
		return "unmarshalable: " + err.Error()
	}
	return string(raw)
}
