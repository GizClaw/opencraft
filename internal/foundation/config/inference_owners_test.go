package config

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func ownedTestConfig(stableIDs ...string) InferenceConfig {
	cfg := InferenceConfig{}
	for _, id := range stableIDs {
		cfg.Instances = append(cfg.Instances, Instance{
			StableID:  id,
			Type:      "deepseek",
			KeySource: KeyEnv,
			Enabled:   true,
		})
	}
	return cfg
}

func TestProviderOwnersSidecarLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := ownedTestConfig("sso-main", "sso-embed")
	owners := map[string]string{
		"sso-main":  "sso-haivivi",
		"sso-embed": "sso-haivivi",
	}
	if err := WriteInferenceOwned(dir, cfg, owners); err != nil {
		t.Fatalf("WriteInferenceOwned: %v", err)
	}
	got, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["sso-main"] != "sso-haivivi" || got["sso-embed"] != "sso-haivivi" {
		t.Fatalf("owners = %+v", got)
	}

	// An ordinary settings save reconciles owners: removed rows drop
	// their stale owner records, surviving rows stay owned.
	if err := WriteInference(dir, ownedTestConfig("sso-embed")); err != nil {
		t.Fatalf("WriteInference: %v", err)
	}
	got, err = LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["sso-embed"] != "sso-haivivi" {
		t.Fatalf("owners after reconcile = %+v", got)
	}

	if err := RemoveInferenceConfig(dir); err != nil {
		t.Fatalf("RemoveInferenceConfig: %v", err)
	}
	if _, err := os.Stat(providerOwnersPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("owner sidecar must be removed, stat err = %v", err)
	}
	got, err = LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("owners after remove config = %+v", got)
	}
}

func TestDropProviderOwnersScopedByPlugin(t *testing.T) {
	dir := t.TempDir()
	cfg := ownedTestConfig("a-main", "b-main")
	owners := map[string]string{
		"a-main": "plug-a",
		"b-main": "plug-b",
	}
	if err := WriteInferenceOwned(dir, cfg, owners); err != nil {
		t.Fatalf("WriteInferenceOwned: %v", err)
	}
	dropped, err := DropProviderOwners(dir, "plug-a")
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	got, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["b-main"] != "plug-b" {
		t.Fatalf("owners after drop = %+v", got)
	}
}

// legacyPluginInstance mirrors the inference row a capability plugin
// wrote before the ownership sidecar existed: the host only accepted
// profile ids equal to the plugin id and kept the key reference inside
// the plugin's secret namespace.
func legacyPluginInstance() Instance {
	return Instance{
		StableID:  "sso-haivivi",
		Type:      "deepseek",
		Name:      "Haivivi SSO",
		KeySource: KeyKeychain,
		KeyValue:  "auth/sso-haivivi/token",
		Enabled:   true,
	}
}

func TestLegacyPluginInstanceAdoptedAndRemovableAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	if err := WriteInference(dir, InferenceConfig{
		Instances: []Instance{legacyPluginInstance()},
	}); err != nil {
		t.Fatalf("WriteInference: %v", err)
	}
	// A pre-sidecar deployment has no owner sidecar file at all; strip
	// the sidecar the fixture write just migrated so the upgrade state
	// is exercised from disk.
	if err := os.Remove(providerOwnersPath(dir)); err != nil {
		t.Fatalf("strip sidecar: %v", err)
	}
	if _, err := os.Stat(providerOwnersPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("legacy fixture must start without an owner sidecar, stat err = %v", err)
	}

	owners, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owners["sso-haivivi"] != "sso-haivivi" {
		t.Fatalf("legacy row not adopted: %+v", owners)
	}

	// The owning plugin (id == instance id under the old contract) may
	// then remove the stale row during its post-upgrade sync.
	removed := false
	err = UpdateInferenceState(
		dir,
		func(
			cfg InferenceConfig,
			owners map[string]string,
		) (InferenceConfig, map[string]string, bool, error) {
			out := cfg.Instances[:0]
			for _, in := range cfg.Instances {
				if in.StableID == "sso-haivivi" {
					if owners["sso-haivivi"] != "sso-haivivi" {
						return cfg, owners, false, fmt.Errorf(
							"legacy row owner not adopted: %+v", owners)
					}
					delete(owners, "sso-haivivi")
					removed = true
					continue
				}
				out = append(out, in)
			}
			cfg.Instances = out
			return cfg, owners, removed, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("legacy row was not removed")
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after removal = %+v", cfg.Instances)
	}
}

func TestUserInstanceMatchingPluginIDIsNotAdopted(t *testing.T) {
	dir := t.TempDir()
	// A row whose stable id equals a plugin id but whose credentials do
	// not live in the plugin namespace is a user row, not legacy
	// plugin state; it must not be claimed.
	if err := WriteInference(dir, InferenceConfig{
		Instances: []Instance{{
			StableID:  "sso-haivivi",
			Type:      "deepseek",
			Name:      "user deepseek",
			KeySource: KeyKeychain,
			KeyValue:  "inference/deepseek-user-account",
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatalf("WriteInference: %v", err)
	}
	owners, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("user row was adopted: %+v", owners)
	}
}

func TestUpdateInferenceStateSerializesLoadModifyWrite(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	ownersByID := map[string]string{
		"plug-a-main": "plug-a",
		"plug-b-main": "plug-b",
	}
	for id, owner := range ownersByID {
		id, owner := id, owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- UpdateInferenceState(
				dir,
				func(
					cfg InferenceConfig,
					owners map[string]string,
				) (InferenceConfig, map[string]string, bool, error) {
					for _, in := range cfg.Instances {
						if in.StableID == id {
							return cfg, owners, false, nil
						}
					}
					cfg.Instances = append(cfg.Instances, Instance{
						StableID:  id,
						Type:      "deepseek",
						KeySource: KeyEnv,
						Enabled:   true,
					})
					owners[id] = owner
					return cfg, owners, true, nil
				},
			)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 2 {
		t.Fatalf("instances = %+v, want both concurrent upserts", cfg.Instances)
	}
	owners, err := LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if owners["plug-a-main"] != "plug-a" ||
		owners["plug-b-main"] != "plug-b" {
		t.Fatalf("owners = %+v", owners)
	}
}
