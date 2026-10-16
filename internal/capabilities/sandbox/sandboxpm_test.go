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
	for _, want := range []string{"PATH", "HOME"} {
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

func TestSandboxRunnerWithCacheUsesInjectedRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	cacheRoot := filepath.Join(t.TempDir(), "workspace-state", "cache")
	runner, policy, err := SandboxRunnerWithCache(
		ctx, t.TempDir(), cacheRoot, SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close() }()
	for _, sub := range []string{"go", "tmp"} {
		if info, err := os.Stat(filepath.Join(cacheRoot, sub)); err != nil || !info.IsDir() {
			t.Fatalf("cache subdir %s missing: %v", sub, err)
		}
	}
	if len(policy.Allow) == 0 {
		t.Fatal("expected default env allowlist")
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

// TestSandboxPolicyRoundTripKeepsEmptyAllow pins the wire form of the
// three-state allow list: an explicit empty list must survive
// marshal/unmarshal as "inherit nothing" and resolve to a non-nil empty
// slice, not collapse back into the curated default (which is what an
// omitted field used to do).
func TestSandboxPolicyRoundTripKeepsEmptyAllow(t *testing.T) {
	pol := SandboxPolicy{EnvPolicy: &EnvPolicyConfig{Allow: []string{}}}
	data, err := json.Marshal(pol)
	if err != nil {
		t.Fatal(err)
	}
	var got SandboxPolicy
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.EnvPolicy == nil {
		t.Fatal("env policy did not survive the round trip")
	}
	if got.EnvPolicy.Allow == nil || len(got.EnvPolicy.Allow) != 0 {
		t.Fatalf("empty allow round trip = %#v, want a non-nil empty list",
			got.EnvPolicy.Allow)
	}
	if allow := got.EnvPolicy.resolve().Allow; allow == nil || len(allow) != 0 {
		t.Fatalf("resolved empty allow = %#v, want a non-nil empty list", allow)
	}
}

func TestEnvPolicyInjectOnlyKeepsDefaultAllowlist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	// A section that only injects must not widen inheritance to the
	// whole host environment: to the core a nil Allow means exactly
	// that, so the translation resolves it to the curated default.
	settings := HostSandboxSettings{
		EnvPolicy: &EnvPolicyConfig{
			Inject: map[string]string{"GOCACHE": "/cache/go"},
		},
	}
	env := settings.Env()
	if env.Allow == nil {
		t.Fatal("inject-only env_policy left Allow nil: the child would inherit the full host environment")
	}
	if !reflect.DeepEqual(env.Allow, DefaultEnvPolicy().Allow) {
		t.Fatalf("inject-only allow = %v, want the default allowlist", env.Allow)
	}
	if env.Inject["GOCACHE"] != "/cache/go" {
		t.Fatalf("inject lost: %+v", env.Inject)
	}

	// The wire form handed to the execd child carries the resolved
	// policy, and the child-side translation resolves it the same way.
	wire := settings.SandboxPolicy()
	if wire.EnvPolicy == nil || wire.EnvPolicy.Allow == nil {
		t.Fatalf("wire policy = %+v, want a resolved allowlist", wire.EnvPolicy)
	}
	runner, policy, err := SandboxRunner(ctx, t.TempDir(), wire)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close() }()
	if len(policy.Allow) == 0 || policy.Inject["GOCACHE"] != "/cache/go" {
		t.Fatalf("child policy = %+v", policy)
	}

	// An explicit empty allow list still means "inherit nothing".
	empty := HostSandboxSettings{EnvPolicy: &EnvPolicyConfig{Allow: []string{}}}
	if got := empty.Env(); got.Allow == nil || len(got.Allow) != 0 {
		t.Fatalf("explicit empty allow = %#v, want a non-nil empty list", got.Allow)
	}
	runner2, emptyPolicy, err := SandboxRunner(ctx, t.TempDir(),
		SandboxPolicy{EnvPolicy: &EnvPolicyConfig{Allow: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner2.Close() }()
	if emptyPolicy.Allow == nil || len(emptyPolicy.Allow) != 0 {
		t.Fatalf("child empty allow = %#v, want a non-nil empty list", emptyPolicy.Allow)
	}
}
