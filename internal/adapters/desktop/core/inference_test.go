package core

import (
	"errors"
	"os"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
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
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCore(configDir, t.TempDir(), "")
	c.SetWorkDir(workDir)
	if err := c.RebuildRuntime(c.Shell.Context()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	first := c.ActiveHost()
	if first == nil {
		t.Fatal("no host after the first rebuild")
	}
	c.pluginWrites.markApplied(nil)

	recorder := logcapture.Install(t)
	if err := c.applyPluginInferenceWrite(false); err != nil {
		t.Fatalf("no-op write: %v", err)
	}
	if got := countInvalidations(recorder); got != 0 {
		t.Fatalf("no-op write invalidated the runtime %d times, want 0", got)
	}
	if got := c.ActiveHost(); got != first {
		t.Fatalf("no-op write replaced the host: %p, want %p", got, first)
	}

	if err := c.applyPluginInferenceWrite(true); err != nil {
		t.Fatalf("changed write: %v", err)
	}
	if got := countInvalidations(recorder); got != 1 {
		t.Fatalf("changed write invalidated the runtime %d times, want 1", got)
	}
	if got := c.ActiveHost(); got == nil || got == first {
		t.Fatalf("changed write must reassemble the runtime, host = %p (was %p)",
			got, first)
	}

	// A failed rebuild must not be assumed good: the next no-op write
	// rebuilds and reports the error again. Break the document so that
	// rebuild has nothing to assemble.
	if err := configseed.Write(configDir, config.InferenceConfig{}); err != nil {
		t.Fatal(err)
	}
	c.pluginWrites.markApplied(errRebuild)
	before := countInvalidations(recorder)
	if err := c.applyPluginInferenceWrite(false); err != nil {
		t.Fatalf("no-op write after failure: %v", err)
	}
	if got := countInvalidations(recorder); got <= before {
		t.Fatal("a no-op write after a failed rebuild must retry it")
	}
	if got := c.ActiveHost(); got != nil {
		t.Fatalf("an unconfigured rebuild left host %p in the pool", got)
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
