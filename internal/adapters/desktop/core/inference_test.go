package core

import (
	"errors"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// errRebuild stands in for a runtime rebuild that failed.
var errRebuild = errors.New("rebuild failed")

// TestPluginInferenceWriteSkipsNoOpRebuild pins the rule that keeps a
// plugin's per-row sync from assembling the runtime once per row: a
// write that leaves the stored rows identical is skipped while the last
// rebuild succeeded and a live Host serves the active workspace. A
// changed write, or a no-op after a failed rebuild, still rebuilds so
// the plugin gets the error back.
func TestPluginInferenceWriteSkipsNoOpRebuild(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")
	// A zero Host is enough: the skip path only reads WorkDir and the
	// stale/closing flags, and RebuildRuntime clears the current Host.
	c.Runtime.current = &host.Host{}
	c.pluginWrites.markApplied(nil)

	if err := c.applyPluginInferenceWrite(false); err != nil {
		t.Fatalf("no-op write: %v", err)
	}
	if c.Runtime.Current() == nil {
		t.Fatal("no-op write rebuilt the runtime")
	}

	if err := c.applyPluginInferenceWrite(true); err != nil {
		t.Fatalf("changed write: %v", err)
	}
	if c.Runtime.Current() != nil {
		t.Fatal("changed write must rebuild the runtime")
	}

	// A failed rebuild must not be assumed good: the next no-op write
	// rebuilds and reports the error again.
	c.Runtime.current = &host.Host{}
	c.pluginWrites.markApplied(errRebuild)
	if err := c.applyPluginInferenceWrite(false); err != nil {
		t.Fatalf("no-op write after failure: %v", err)
	}
	if c.Runtime.Current() != nil {
		t.Fatal("a no-op write after a failed rebuild must retry it")
	}
}

// TestRemovePluginInferenceValidatesPluginID pins the rule that stayed
// in the adapter: the plugin registry id is validated before the config
// layer is touched.
func TestRemovePluginInferenceValidatesPluginID(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")
	if _, err := c.RemovePluginInference("Bad Id"); err == nil {
		t.Fatal("invalid plugin id accepted")
	}
}

// TestRemovePluginInferenceDelegatesToConfig pins the cleanup path a
// plugin disable/uninstall takes: every row the plugin owns disappears
// and the ownership sidecar is cleaned up.
func TestRemovePluginInferenceDelegatesToConfig(t *testing.T) {
	dir := t.TempDir()
	c := NewCore(dir, dir, "")
	includeUsage := false
	for _, id := range []string{"sso-haivivi-main", "sso-haivivi-glm"} {
		profile := config.InstanceSpec{
			StableID: id,
			Type:     "openai",
			API:      "chat",
			Endpoint: "https://ai.example.com/v1",
			KeyRef:   "auth/sso-haivivi/token",
			Advanced: config.InstanceAdvanced{ChatIncludeUsage: &includeUsage},
			Models:   []config.ModelSpec{{Name: "deepseek-v4-flash"}},
		}
		if _, err := config.UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	removed, err := c.RemovePluginInference("sso-haivivi")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("RemovePluginInference must report a removed row")
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after plugin remove = %+v", cfg.Instances)
	}
	owners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after plugin remove = %+v", owners)
	}
}
