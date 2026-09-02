package toolchain

import "testing"

func TestLaunchEnvForBundledGo(t *testing.T) {
	env := withLaunchEnv(
		[]string{"PATH=/usr/bin", "GOROOT=/opt/go"},
		launchEnvFor(&Runtime{
			Family: "go",
			Root:   "/app/runtime/go/1.25/darwin-arm64",
		}),
	)
	got := envMap(env)
	if got["GOROOT"] != "/opt/go" {
		t.Fatalf("explicit GOROOT must win, got %q", got["GOROOT"])
	}
	if got["GOTOOLCHAIN"] != "local" {
		t.Fatalf("GOTOOLCHAIN = %q, want local", got["GOTOOLCHAIN"])
	}
}

func TestLauncherMainUnknownToolFails(t *testing.T) {
	if code := LauncherMain([]string{"runtime-launcher", "no-such-tool"}); code == 0 {
		t.Fatal("unknown tool must fail")
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				out[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return out
}
