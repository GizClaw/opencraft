package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

func TestSandboxRunnerEmptyPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	runner, policy, err := SandboxRunner(ctx, t.TempDir(), SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close() }()
	// A deployment without env_policy gets the curated default
	// allowlist, never wholesale environment inheritance.
	if len(policy.Allow) == 0 {
		t.Fatalf("empty policy = %+v, want a default allowlist", policy)
	}
	allow := map[string]bool{}
	for _, name := range policy.Allow {
		allow[name] = true
	}
	for _, want := range []string{"PATH", "HOME", "OPEN_CRAFT_CACHE"} {
		if !allow[want] {
			t.Errorf("default allowlist missing %q: %v", want, policy.Allow)
		}
	}
	for _, secret := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if allow[secret] {
			t.Errorf("default allowlist must not expose %q", secret)
		}
	}
	if !reflect.DeepEqual(policy, DefaultEnvPolicy()) {
		t.Errorf("empty policy = %+v, want DefaultEnvPolicy()", policy)
	}
}

func TestSandboxRunnerConfiguredPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	extra := filepath.Join(t.TempDir(), "extra")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	pol := SandboxPolicy{
		WritablePaths: []string{extra},
		EnvPolicy: &EnvPolicyConfig{
			Allow:  []string{"PATH", "GOPROXY"},
			Inject: map[string]string{"GOMODCACHE": "/cache/pkg/mod"},
		},
	}
	runner, policy, err := SandboxRunner(ctx, t.TempDir(), pol)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close() }()
	want := coresandbox.EnvPolicy{
		Allow:  pol.EnvPolicy.Allow,
		Inject: pol.EnvPolicy.Inject,
	}
	if !reflect.DeepEqual(policy, want) {
		t.Errorf("configured policy = %+v, want %+v", policy, want)
	}
}

func TestSandboxPolicyRoundTrip(t *testing.T) {
	pol := SandboxPolicy{
		WritablePaths: []string{"/tmp/w"},
		EnvPolicy: &EnvPolicyConfig{
			Allow:  []string{"PATH", "HOME"},
			Inject: map[string]string{"GOCACHE": "/cache/go"},
		},
	}
	data, err := json.Marshal(pol)
	if err != nil {
		t.Fatal(err)
	}
	var got SandboxPolicy
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, pol) {
		t.Errorf("round trip = %+v, want %+v", got, pol)
	}
}
