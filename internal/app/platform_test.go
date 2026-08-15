package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
)

func TestSandboxSettingsDecodeAndPolicy(t *testing.T) {
	s, err := resource.DecodeTyped[sandboxSettings]([]byte(`{
		"root": "/ws",
		"writable_paths": ["/ws/cache"],
		"remote": true,
		"env_policy": {
			"allow": ["PATH", "HOME"],
			"inject": {"GOCACHE": "/ws/cache/go", "TMPDIR": "/ws/cache/tmp"}
		}
	}`), resource.ExpandEnv())
	if err != nil {
		t.Fatal(err)
	}
	if s.EnvPolicy == nil {
		t.Fatal("env_policy not decoded")
	}
	want := SandboxPolicy{
		WritablePaths: []string{"/ws/cache"},
		EnvPolicy: &EnvPolicyConfig{
			Allow: []string{"PATH", "HOME"},
			Inject: map[string]string{
				"GOCACHE": "/ws/cache/go",
				"TMPDIR":  "/ws/cache/tmp",
			},
		},
	}
	if got := s.sandboxPolicy(); !reflect.DeepEqual(got, want) {
		t.Errorf("sandboxPolicy = %+v, want %+v", got, want)
	}
}

func TestSandboxSettingsNoEnvPolicyFallsBack(t *testing.T) {
	s, err := resource.DecodeTyped[sandboxSettings]([]byte(`{
		"root": "/ws",
		"remote": true
	}`), resource.ExpandEnv())
	if err != nil {
		t.Fatal(err)
	}
	if s.EnvPolicy != nil {
		t.Fatalf("env_policy = %+v, want nil", s.EnvPolicy)
	}
	pol := s.sandboxPolicy()
	if pol.EnvPolicy != nil {
		t.Fatalf("policy env = %+v, want nil (child fallback)", pol.EnvPolicy)
	}
	if len(pol.WritablePaths) != 0 {
		t.Fatalf("writable paths = %v, want none", pol.WritablePaths)
	}
}

func TestLocalSandboxAppliesEnvPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mgr, err := New([]string{"env *"}, "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{
		"root":             t.TempDir(),
		"remote":           false,
		"env_policy": map[string]any{
			"allow":  []string{"PATH"},
			"inject": map[string]string{"OPENCRAFT_TEST_MARKER": "policy-ok"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := (sandboxFactory{}).New(
		context.Background(), resource.Input{
			Settings: settings,
			Deps:     map[string]any{"execpolicy": mgr},
		})
	if err != nil {
		t.Skipf("sandbox backend unavailable: %v", err)
	}
	runner, ok := value.(sandbox.Runner)
	if !ok {
		t.Fatalf("factory value = %T, want sandbox.Runner", value)
	}
	defer func() { _ = runner.Close() }()

	res, err := sandbox.Exec(context.Background(), runner,
		"/bin/sh", []string{"-c", "env"}, sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "OPENCRAFT_TEST_MARKER=policy-ok") {
		t.Fatalf("stdout = %q, want injected env var", res.Stdout)
	}
	if strings.Contains(res.Stdout, "HOME=") {
		t.Errorf("stdout = %q, HOME leaked through the allow filter", res.Stdout)
	}
	// The policy manager must expose the allowlist rules for worldstate.
	if rules := mgr.Rules(); len(rules) != 1 || rules[0] != "env *" {
		t.Fatalf("policy rules = %v, want [\"env *\"]", rules)
	}
}
