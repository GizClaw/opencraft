package config

import (
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
