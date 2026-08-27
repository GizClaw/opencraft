package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, userDir, content string) {
	t.Helper()
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(userDir, "opencraft.yaml"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateCommandPolicy(t *testing.T) {
	wd := t.TempDir()
	userDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, userDir, `version: v1
resources:
  execpolicy:
    settings:
      allowed_commands:
        - git status
`)
	app := &App{workDir: wd, userDir: userDir}

	allowed, err := app.EvaluateCommandPolicy("git status")
	if err != nil {
		t.Fatalf("EvaluateCommandPolicy: %v", err)
	}
	if !allowed.Allowed {
		t.Fatalf("git status must be allowed by policy: %+v", allowed)
	}

	blocked, err := app.EvaluateCommandPolicy("rm -rf /tmp/x")
	if err != nil {
		t.Fatalf("EvaluateCommandPolicy: %v", err)
	}
	if blocked.Allowed {
		t.Fatalf("rm -rf must not be allowed: %+v", blocked)
	}

	if _, err := app.EvaluateCommandPolicy("   "); err == nil {
		t.Fatal("empty command must be rejected")
	}
}

func TestEvaluateCommandPolicyReadsApprovalsFile(t *testing.T) {
	wd := t.TempDir()
	userDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, userDir, "version: v1\n")
	approvalsDir := filepath.Join(wd, ".opencraft", "config")
	if err := os.MkdirAll(approvalsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(approvalsDir, "approvals.yaml"),
		[]byte("version: v1\nallow:\n  - npm install\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	app := &App{workDir: wd, userDir: userDir}
	decision, err := app.EvaluateCommandPolicy("npm install")
	if err != nil {
		t.Fatalf("EvaluateCommandPolicy: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("npm install must be allowed via approvals file: %+v", decision)
	}
}

func TestDiagnosticsSmoke(t *testing.T) {
	app := &App{
		workDir: t.TempDir(),
		userDir: filepath.Join(t.TempDir(), "config"),
	}
	rep := app.Diagnostics()
	if rep.Version == "" || rep.GoVersion == "" || rep.Platform == "" {
		t.Fatalf("diagnostics missing basics: %+v", rep)
	}
	if rep.SandboxBackend == "" {
		t.Fatalf("sandbox backend must be reported: %+v", rep)
	}
}
