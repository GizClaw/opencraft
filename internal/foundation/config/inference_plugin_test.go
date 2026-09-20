package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
)

// pluginSpec is one plugin-submitted row in the shape a capability
// plugin sends over inference.upsert: an identity of its own and a
// credential reference inside its secret namespace.
func pluginSpec(pluginID, id, typ string) InstanceSpec {
	return InstanceSpec{
		StableID: id,
		Type:     typ,
		Name:     id,
		API:      "responses",
		Endpoint: "https://ai.example.com/v1",
		KeyRef:   "auth/" + pluginID + "/token",
		Models:   []ModelSpec{{Name: "some-model"}},
	}
}

// TestPluginInstanceDeclaresNewVendor covers a plugin that brings its
// own provider: naming a driver lets it introduce a vendor that is not
// in the preset list, and a row with neither a known type nor a driver
// is refused.
func TestPluginInstanceDeclaresNewVendor(t *testing.T) {
	dir := t.TempDir()

	unnamed := InstanceSpec{
		StableID: "vendorx-main",
		Type:     "vendorx",
		API:      "chat",
		Endpoint: "https://api.vendorx.example/v1",
		KeyRef:   "auth/vendorx/token",
		Models:   []ModelSpec{{Name: "vendorx-pro"}},
	}
	if _, err := UpsertPluginInstance(dir, "vendorx", unnamed); err == nil {
		t.Fatal("a provider outside the presets must name its driver")
	}

	declared := unnamed
	declared.Driver = "openai"
	if _, err := UpsertPluginInstance(dir, "vendorx", declared); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	in := cfg.Instances[0]
	if in.Type != "vendorx" || in.Driver != "openai" {
		t.Fatalf("declared provider = %+v", in)
	}
	prov, ok := ProviderFor(in)
	if !ok || prov.Impl != "openai" {
		t.Fatalf("resolved provider = %+v (ok=%v)", prov, ok)
	}
}

// TestPluginInstanceReportsNoOpWrite pins the signal the desktop host
// uses to decide whether a plugin write needs a runtime rebuild: a
// plugin re-submits its whole row set on every catalog sync, so an
// identical row must report no change, while a new row, an edited row
// and a real removal must.
func TestPluginInstanceReportsNoOpWrite(t *testing.T) {
	dir := t.TempDir()
	profile := pluginSpec("sso-haivivi", "sso-haivivi-main", "openai")

	changed, err := UpsertPluginInstance(dir, "sso-haivivi", profile)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !changed {
		t.Fatal("first upsert must report a change")
	}

	changed, err = UpsertPluginInstance(dir, "sso-haivivi", profile)
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if changed {
		t.Fatal("identical re-upsert must not report a change")
	}

	edited := pluginSpec("sso-haivivi", "sso-haivivi-main", "openai")
	edited.Models = append(edited.Models, ModelSpec{Name: "glm-5.3-flash"})
	changed, err = UpsertPluginInstance(dir, "sso-haivivi", edited)
	if err != nil {
		t.Fatalf("edited upsert: %v", err)
	}
	if !changed {
		t.Fatal("edited row must report a change")
	}

	changed, err = RemovePluginInstance(dir, "sso-haivivi", "sso-haivivi-absent")
	if err != nil {
		t.Fatalf("remove missing row: %v", err)
	}
	if changed {
		t.Fatal("removing an absent row must not report a change")
	}

	changed, err = RemovePluginInstance(dir, "sso-haivivi", "sso-haivivi-main")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !changed {
		t.Fatal("removing a stored row must report a change")
	}
}

// TestPluginInstanceUpsertAndRemove pins the identity/ownership round
// trip of one plugin row.
func TestPluginInstanceUpsertAndRemove(t *testing.T) {
	dir := t.TempDir()
	profile := pluginSpec("sso-haivivi", "sso-haivivi-main", "openai")
	profile.API = "responses"
	profile.Models = []ModelSpec{{
		Name: "deepseek-v4-flash",
		Capabilities: model.ModelCapabilities{
			Reasoning: model.ReasoningCapability{Kind: model.ReasoningToggle},
		},
	}}
	if _, err := UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	in := cfg.Instances[0]
	if in.StableID != "sso-haivivi-main" || in.Type != "openai" ||
		!in.Enabled || in.KeySource != KeyKeychain ||
		in.KeyValue != "auth/sso-haivivi/token" ||
		len(in.Models) != 1 || in.Models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("inference instance = %+v", in)
	}
	owners, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owners["sso-haivivi-main"] != "sso-haivivi" {
		t.Fatalf("owner not recorded: %+v", owners)
	}

	if _, err := RemovePluginInstance(dir, "sso-haivivi", "sso-haivivi-main"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, err = LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after remove = %+v", cfg.Instances)
	}
	owners, err = LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after remove = %+v, want empty", owners)
	}
}

// TestPluginInstancePreservesModelKindAndLimits pins that the plugin
// path carries the canonical model declaration: kind, capability kinds
// and the context limits reach the stored model unchanged.
func TestPluginInstancePreservesModelKindAndLimits(t *testing.T) {
	dir := t.TempDir()
	maxInput, maxOutput := 1_000_000, 65_536
	profile := pluginSpec("plug", "plug-custom", "openai")
	profile.Models = []ModelSpec{{
		Name: "custom-llm",
		Kind: "generate",
		Capabilities: model.ModelCapabilities{
			Inputs:    []message.PartKind{message.PartText},
			Outputs:   []message.PartKind{message.PartText},
			Reasoning: model.ReasoningCapability{Kind: model.ReasoningToggle},
		},
		Limits: model.ModelLimits{
			MaxInputTokens:  &maxInput,
			MaxOutputTokens: &maxOutput,
		},
	}}
	if _, err := UpsertPluginInstance(dir, "plug", profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 || len(cfg.Instances[0].Models) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	m := cfg.Instances[0].Models[0]
	if m.Name != "custom-llm" || m.Kind != "generate" {
		t.Fatalf("model = %+v, want custom-llm/generate", m)
	}
	if m.Limits.MaxInputTokens == nil ||
		*m.Limits.MaxInputTokens != maxInput ||
		m.Limits.MaxOutputTokens == nil ||
		*m.Limits.MaxOutputTokens != maxOutput {
		t.Fatalf("limits = %+v, want %d/%d", m.Limits, maxInput, maxOutput)
	}
}

// TestPluginInstanceAdvancedKnobs covers the typed provider knobs: a
// plugin configures the same fields the settings page edits, and they
// reach the document the driver decodes.
func TestPluginInstanceAdvancedKnobs(t *testing.T) {
	dir := t.TempDir()
	includeUsage := false
	profile := pluginSpec("sso-haivivi", "sso-haivivi-glm", "openai")
	profile.API = "chat"
	profile.Models = []ModelSpec{{
		Name: "glm-5.3-flash",
		Capabilities: model.ModelCapabilities{
			Reasoning: model.ReasoningCapability{Kind: model.ReasoningAlways},
		},
	}}
	profile.Advanced = InstanceAdvanced{
		ChatIncludeUsage: &includeUsage,
		Store:            "omit",
		ReasoningScope:   "gateway-2026",
	}
	if _, err := UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	adv := cfg.Instances[0].Advanced
	if adv.ChatIncludeUsage == nil || *adv.ChatIncludeUsage ||
		adv.Store != "omit" || adv.ReasoningScope != "gateway-2026" {
		t.Fatalf("advanced = %+v", adv)
	}
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"api: 'chat'",
		"reasoning_scope: 'gateway-2026'",
		"chat_stream_options:",
		"include_usage: false",
		"store: omit",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("provider spec missing %q:\n%s", want, doc)
		}
	}
}

// TestPluginInstanceAdvancedValidation pins the constraints opencraft
// enforces before the driver sees the document.
func TestPluginInstanceAdvancedValidation(t *testing.T) {
	dir := t.TempDir()
	includeUsage := false

	chatOptions := func(typ, api string) error {
		profile := pluginSpec("sso-haivivi", "sso-haivivi-glm", typ)
		profile.API = api
		profile.Advanced = InstanceAdvanced{ChatIncludeUsage: &includeUsage}
		_, err := UpsertPluginInstance(dir, "sso-haivivi", profile)
		return err
	}
	if err := chatOptions("openai", "chat"); err != nil {
		t.Fatalf("valid openai chat profile rejected: %v", err)
	}
	if err := chatOptions("anthropic", ""); err == nil ||
		!strings.Contains(err.Error(), "OpenAI-wire chat") {
		t.Fatalf("chat options on a non-openai deployment = %v", err)
	}
	if err := chatOptions("openai", "responses"); err == nil ||
		!strings.Contains(err.Error(), "OpenAI-wire chat") {
		t.Fatalf("chat options on a responses deployment = %v", err)
	}

	badStore := pluginSpec("sso-haivivi", "sso-haivivi-store", "openai")
	badStore.Advanced = InstanceAdvanced{Store: "sometimes"}
	if _, err := UpsertPluginInstance(dir, "sso-haivivi", badStore); err == nil ||
		!strings.Contains(err.Error(), "store") {
		t.Fatalf("unknown store policy accepted: %v", err)
	}
}

// TestPluginInstanceSourcePolicy pins what a plugin row may state: a
// credential inside its own namespace, and no user-owned fields.
func TestPluginInstanceSourcePolicy(t *testing.T) {
	dir := t.TempDir()

	foreign := pluginSpec("plug", "plug-gateway", "openai")
	foreign.KeyRef = "auth/other-plugin/token"
	if _, err := UpsertPluginInstance(dir, "plug", foreign); err == nil ||
		!strings.Contains(err.Error(), "outside plugin namespace") {
		t.Fatalf("foreign key ref accepted: %v", err)
	}

	literal := pluginSpec("plug", "plug-gateway", "openai")
	literal.KeyValue = "sk-plaintext"
	if _, err := UpsertPluginInstance(dir, "plug", literal); err == nil ||
		!strings.Contains(err.Error(), "literal key") {
		t.Fatalf("plugin literal key accepted: %v", err)
	}

	envSource := pluginSpec("plug", "plug-gateway", "openai")
	envSource.KeySource = KeySourceEnvName
	if _, err := UpsertPluginInstance(dir, "plug", envSource); err == nil ||
		!strings.Contains(err.Error(), "not available to plugins") {
		t.Fatalf("plugin env source accepted: %v", err)
	}

	disabled := pluginSpec("plug", "plug-gateway", "openai")
	off := false
	disabled.Enabled = &off
	if _, err := UpsertPluginInstance(dir, "plug", disabled); err == nil ||
		!strings.Contains(err.Error(), "user-owned") {
		t.Fatalf("plugin disabled its own row: %v", err)
	}

	badID := pluginSpec("plug", "Plug-Gateway", "openai")
	if _, err := UpsertPluginInstance(dir, "plug", badID); err == nil ||
		!strings.Contains(err.Error(), "invalid provider instance id") {
		t.Fatalf("malformed identity accepted: %v", err)
	}
}

// TestPluginInstanceCannotHijackAnotherInstance pins the ownership
// guard: a row written by one plugin cannot be replaced by another.
func TestPluginInstanceCannotHijackAnotherInstance(t *testing.T) {
	dir := t.TempDir()
	profile := pluginSpec("sso-haivivi", "sso-haivivi-gateway", "openai")
	if _, err := UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	other := profile
	other.KeyRef = "auth/other-plugin/token"
	if _, err := UpsertPluginInstance(dir, "other-plugin", other); err == nil ||
		!strings.Contains(err.Error(), "owned by plugin") {
		t.Fatalf("foreign plugin must not hijack an owned id, got %v", err)
	}
}

// TestManagedRowEnabledStaysUserOwned pins the ownership split of a
// plugin row: the plugin owns the deployment content, the user owns
// whether it is routed, and neither overwrites the other.
func TestManagedRowEnabledStaysUserOwned(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	dir := t.TempDir()
	pluginID, id := "sso-haivivi", "sso-haivivi-main"
	profile := pluginSpec(pluginID, id, "openai")
	if _, err := UpsertPluginInstance(dir, pluginID, profile); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The user turns the row off from the settings page; the plugin's
	// content survives the round trip.
	off := false
	item := pluginSpec(pluginID, id, "openai")
	item.Enabled = &off
	user := InstanceSpec{
		Type:      "openai",
		API:       "responses",
		KeySource: KeySourceEnvName,
		Enabled:   boolPtr(true),
		Models:    []ModelSpec{{Name: "gpt-5.6-sol"}},
	}
	if _, err := ApplySettingsSave(dir, SaveRequest{
		Instances: []InstanceSpec{item, user},
	}, func(string) bool { return true }); err != nil {
		t.Fatalf("settings save: %v", err)
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	var managed Instance
	for _, in := range cfg.Instances {
		if in.StableID == id {
			managed = in
		}
	}
	if managed.StableID != id || managed.Enabled {
		t.Fatalf("user toggle lost: %+v", cfg.Instances)
	}

	// A later plugin upsert refreshes the content without re-enabling
	// the row behind the user's back.
	refreshed := pluginSpec(pluginID, id, "openai")
	refreshed.Endpoint = "https://ai.example.com/v2"
	if _, err := UpsertPluginInstance(dir, pluginID, refreshed); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	cfg, err = LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range cfg.Instances {
		if in.StableID != id {
			continue
		}
		if in.Enabled || in.Endpoint != "https://ai.example.com/v2" {
			t.Fatalf("re-upsert state = %+v", in)
		}
		return
	}
	t.Fatalf("plugin row dropped: %+v", cfg.Instances)
}

// TestPluginInstanceRequiresExplicitOwnership pins that a user row is
// never silently claimed by a plugin that happens to pick the same id.
func TestPluginInstanceRequiresExplicitOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := seedInferenceOwned(dir, InferenceConfig{
		Instances: []Instance{{
			StableID:  "sso-haivivi-main",
			Type:      "openai",
			KeySource: KeyLiteral,
			KeyValue:  "user-key",
			Enabled:   true,
			Models:    []Model{{Name: "deepseek-v4-flash"}},
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	profile := pluginSpec("sso-haivivi", "sso-haivivi-main", "openai")
	if _, err := UpsertPluginInstance(dir, "sso-haivivi", profile); err == nil ||
		!strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned instance must not be claimed, got %v", err)
	}
}

// TestPluginInstancesCleanLegacyPreOwnershipInstance covers the upgrade
// path: rows written under the single-instance plugin contract (id ==
// plugin id, key inside the plugin namespace, no sidecar) are adopted so
// the plugin can replace and remove its stale deployment.
func TestPluginInstancesCleanLegacyPreOwnershipInstance(t *testing.T) {
	dir := t.TempDir()
	if err := seedInference(dir, InferenceConfig{
		Instances: []Instance{{
			StableID:  "sso-haivivi",
			Type:      "openai",
			Name:      "Haivivi SSO",
			KeySource: KeyKeychain,
			KeyValue:  "auth/sso-haivivi/token",
			Enabled:   true,
			Models:    []Model{{Name: "deepseek-v4-flash"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// A pre-sidecar deployment has no owner sidecar file at all; strip
	// the sidecar the fixture write just migrated.
	if err := os.Remove(filepath.Join(dir, providerOwnersFileName)); err != nil {
		t.Fatalf("strip sidecar: %v", err)
	}

	profile := pluginSpec("sso-haivivi", "sso-haivivi-deepseek", "openai")
	profile.Name = "Haivivi SSO"
	if _, err := UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
		t.Fatalf("upsert new profile: %v", err)
	}
	if _, err := RemovePluginInstance(dir, "sso-haivivi", "sso-haivivi"); err != nil {
		t.Fatalf("remove legacy instance after upgrade: %v", err)
	}

	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 ||
		cfg.Instances[0].StableID != "sso-haivivi-deepseek" {
		t.Fatalf("instances after cleanup = %+v, want only the new profile",
			cfg.Instances)
	}
	owners, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 1 || owners["sso-haivivi-deepseek"] != "sso-haivivi" {
		t.Fatalf("owners after cleanup = %+v", owners)
	}
}

// TestPluginInstancesRemoveAll covers the disable/uninstall fallback.
func TestPluginInstancesRemoveAll(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"sso-haivivi-gateway", "sso-haivivi-embed"} {
		profile := pluginSpec("sso-haivivi", id, "openai")
		if _, err := UpsertPluginInstance(dir, "sso-haivivi", profile); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	removed, err := RemovePluginInstances(dir, "sso-haivivi")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("RemovePluginInstances must report a removed row")
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after plugin remove = %+v", cfg.Instances)
	}
	owners, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after plugin remove = %+v", owners)
	}
}
