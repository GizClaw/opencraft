package bindings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// writeExecutable drops an executable file into dir and returns its
// path.
func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestMCPCommandProblemAcceptsRunnableCommand(t *testing.T) {
	exe := writeExecutable(t, t.TempDir(), "server", "#!/bin/sh\n")
	if err := mcpCommandProblem(config.MCPServer{
		Name: "server", Transport: "stdio", Command: exe,
	}); err != nil {
		t.Fatalf("mcpCommandProblem(absolute executable) = %v, want nil", err)
	}
	// HTTP servers spawn nothing, so the command check stays out of
	// their way.
	if err := mcpCommandProblem(config.MCPServer{
		Name: "remote", Transport: "http", URL: "https://example.test/mcp",
	}); err != nil {
		t.Fatalf("mcpCommandProblem(http) = %v, want nil", err)
	}
}

// TestMCPCommandProblemResolvesBareNameAgainstAppPATH pins the rule
// that bit the desktop app: os/exec resolves a bare command name with
// LookPath before the child exists, so it reads this process's PATH.
func TestMCPCommandProblemResolvesBareNameAgainstAppPATH(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "rive-mcp", "#!/bin/sh\n")
	t.Setenv("PATH", binDir)

	if err := mcpCommandProblem(config.MCPServer{
		Name: "rive-mcp", Transport: "stdio", Command: "rive-mcp",
	}); err != nil {
		t.Fatalf("mcpCommandProblem(bare name on PATH) = %v, want nil", err)
	}

	t.Setenv("PATH", t.TempDir())
	err := mcpCommandProblem(config.MCPServer{
		Name: "rive-mcp", Transport: "stdio", Command: "rive-mcp",
	})
	if err == nil {
		t.Fatal("mcpCommandProblem(bare name off PATH) = nil, want error")
	}
	for _, want := range []string{"not found in this app's PATH", "absolute command path"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// TestMCPCommandProblemBareNameIgnoresEnvPATH documents that a
// configured env.PATH cannot fix a bare command name: it only shapes the
// child's environment.
func TestMCPCommandProblemBareNameIgnoresEnvPATH(t *testing.T) {
	envDir := t.TempDir()
	writeExecutable(t, envDir, "rive-mcp", "#!/bin/sh\n")
	t.Setenv("PATH", t.TempDir())

	err := mcpCommandProblem(config.MCPServer{
		Name:      "rive-mcp",
		Transport: "stdio",
		Command:   "rive-mcp",
		Env:       map[string]string{"PATH": envDir},
	})
	if err == nil {
		t.Fatal("mcpCommandProblem(bare name reachable only via env.PATH) = nil, want error")
	}
}

func TestMCPCommandProblemRejectsUnusablePath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing-server")
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "missing", path: missing, want: "no such file"},
		{name: "directory", path: dir, want: "is a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mcpCommandProblem(config.MCPServer{
				Name: "server", Transport: "stdio", Command: tt.path,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("mcpCommandProblem(%s) = %v, want error containing %q",
					tt.path, err, tt.want)
			}
		})
	}

	if runtime.GOOS == "windows" {
		return
	}
	t.Run("not executable", func(t *testing.T) {
		path := filepath.Join(dir, "plain-server")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		err := mcpCommandProblem(config.MCPServer{
			Name: "server", Transport: "stdio", Command: path,
		})
		if err == nil || !strings.Contains(err.Error(), "is not executable") {
			t.Fatalf("mcpCommandProblem(%s) = %v, want not-executable error", path, err)
		}
	})
}

// TestMCPCommandProblemChecksShebangInterpreter covers the second half
// of the same failure: an absolute command whose `#!/usr/bin/env node`
// shebang still needs node on the *server's* PATH.
func TestMCPCommandProblemChecksShebangInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not start scripts through shebangs")
	}
	interpDir := t.TempDir()
	writeExecutable(t, interpDir, "node", "#!/bin/sh\n")
	script := writeExecutable(t, t.TempDir(), "rive-mcp", "#!/usr/bin/env node\n")
	server := config.MCPServer{
		Name: "rive-mcp", Transport: "stdio", Command: script,
	}

	// env.PATH replaces the inherited PATH, so the interpreter is found.
	server.Env = map[string]string{"PATH": interpDir}
	if err := mcpCommandProblem(server); err != nil {
		t.Fatalf("mcpCommandProblem(interpreter on env.PATH) = %v, want nil", err)
	}

	// Without env.PATH the child inherits this process's PATH.
	server.Env = nil
	t.Setenv("PATH", interpDir)
	if err := mcpCommandProblem(server); err != nil {
		t.Fatalf("mcpCommandProblem(interpreter on inherited PATH) = %v, want nil", err)
	}

	t.Setenv("PATH", "/usr/bin:/bin")
	server.Env = map[string]string{"PATH": t.TempDir()}
	err := mcpCommandProblem(server)
	if err == nil {
		t.Fatal("mcpCommandProblem(interpreter missing) = nil, want error")
	}
	if !strings.Contains(err.Error(), `needs "node"`) ||
		!strings.Contains(err.Error(), "env.PATH") {
		t.Fatalf("error %q does not explain the env.PATH fix", err)
	}
}

func TestMCPCommandProblemReportsMissingShebangInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not start scripts through shebangs")
	}
	script := writeExecutable(t, t.TempDir(), "server", "#!/nonexistent/interp\n")
	err := mcpCommandProblem(config.MCPServer{
		Name: "server", Transport: "stdio", Command: script,
	})
	if err == nil || !strings.Contains(err.Error(), "shebang interpreter") {
		t.Fatalf("mcpCommandProblem(missing interpreter) = %v, want shebang error", err)
	}
}

func TestReadShebang(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name        string
		body        string
		interpreter string
		argument    string
		ok          bool
	}{
		{name: "env", body: "#!/usr/bin/env node\n", interpreter: "/usr/bin/env", argument: "node", ok: true},
		{name: "env flags", body: "#!/usr/bin/env -S node --flag\n", interpreter: "/usr/bin/env", argument: "node", ok: true},
		{name: "env assignment", body: "#!/usr/bin/env FOO=bar node\n", interpreter: "/usr/bin/env", argument: "node", ok: true},
		{name: "env only", body: "#!/usr/bin/env\n", interpreter: "/usr/bin/env", ok: true},
		{name: "crlf", body: "#!/usr/bin/env node\r\n", interpreter: "/usr/bin/env", argument: "node", ok: true},
		{name: "absolute", body: "#!/bin/sh\n", interpreter: "/bin/sh", ok: true},
		{name: "not a script", body: "\x7fELF binary\n", ok: false},
		{name: "empty", body: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeExecutable(t, dir, "script-"+strings.ReplaceAll(tt.name, " ", "-"), tt.body)
			interpreter, argument, ok := readShebang(path)
			if ok != tt.ok || interpreter != tt.interpreter || argument != tt.argument {
				t.Fatalf("readShebang(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.body, interpreter, argument, ok, tt.interpreter, tt.argument, tt.ok)
			}
		})
	}
	if _, _, ok := readShebang(filepath.Join(dir, "absent")); ok {
		t.Fatal("readShebang(absent file) reported a shebang")
	}
}

// TestMCPStatusReportsUnspawnableCommand is the regression test for the
// rive-mcp incident: a stdio server whose command is not on the app PATH
// must read "error" with the reason, not "connecting" forever.
func TestMCPStatusReportsUnspawnableCommand(t *testing.T) {
	dir := t.TempDir()
	if err := config.WriteMCP(dir, []config.MCPServer{{
		Name: "rive-mcp", Transport: "stdio", Command: "rive-mcp",
	}}); err != nil {
		t.Fatalf("WriteMCP: %v", err)
	}
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	statuses, err := NewConfig(core.NewCore(dir, dir, "")).MCPStatus()
	if err != nil {
		t.Fatalf("MCPStatus: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("MCPStatus returned %d entries, want 1", len(statuses))
	}
	if statuses[0].Status != "error" {
		t.Fatalf("status = %q, want error", statuses[0].Status)
	}
	if !strings.Contains(statuses[0].Error, "rive-mcp") {
		t.Fatalf("error %q does not name the server command", statuses[0].Error)
	}
}

// TestMCPStatusStaysConnectingForSpawnableCommand guards against a
// false positive: a command this process can spawn keeps the readiness
// probe's own verdict.
func TestMCPStatusStaysConnectingForSpawnableCommand(t *testing.T) {
	dir := t.TempDir()
	exe := writeExecutable(t, t.TempDir(), "server", "#!/bin/sh\n")
	if err := config.WriteMCP(dir, []config.MCPServer{{
		Name: "server", Transport: "stdio", Command: exe,
	}}); err != nil {
		t.Fatalf("WriteMCP: %v", err)
	}

	statuses, err := NewConfig(core.NewCore(dir, dir, "")).MCPStatus()
	if err != nil {
		t.Fatalf("MCPStatus: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != "connecting" {
		t.Fatalf("MCPStatus = %+v, want a single connecting server", statuses)
	}
}
