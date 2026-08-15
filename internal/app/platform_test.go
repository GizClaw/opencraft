package app

import (
	"reflect"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
)

func TestSandboxSettingsDecodeAndPolicy(t *testing.T) {
	s, err := resource.DecodeTyped[sandboxSettings]([]byte(`{
		"root": "/ws",
		"writable_paths": ["/ws/cache"],
		"remote": true,
		"allowed_commands": ["ls *"],
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
