// Package shelldetect picks the shell that command strings are handed
// to on a platform, together with the characters that stay unambiguous
// in a bare word without a shell.
//
// It is shared: the exec tools spawn through the returned spec, and the
// sandbox child reports the same program through its environment info,
// so the description the model reads, the shell that actually runs, and
// the diagnostics page cannot drift apart.
package shelldetect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Spec is one shell invocation: the program, the flags that must
// precede the script, and the extra word characters the platform treats
// as unremarkable.
type Spec struct {
	// Program is the shell executable, e.g. "/bin/sh" or a resolved
	// powershell.exe path.
	Program string
	// Args are the flags that precede the script, e.g. "-c", "/c", or
	// "-NoProfile -Command".
	Args []string
	// SafeRunes lists characters beyond [A-Za-z0-9_./:+-] that are
	// unambiguous in a bare word. Windows adds its path separator; on
	// POSIX a backslash is an escape character, so accepting it there
	// would silently change what the shell path means.
	SafeRunes string
}

// CommandLine renders the invocation for tool descriptions, prompts,
// and diagnostics ("/bin/sh -c", "pwsh.exe -NoProfile -Command").
func (s Spec) CommandLine() string {
	return strings.Join(append([]string{s.Program}, s.Args...), " ")
}

// Default returns the conventional shell for goos without probing the
// host. Windows has no /bin/sh, so shell syntax there falls back to
// cmd.exe.
func Default(goos string) Spec {
	if goos == "windows" {
		return cmdSpec("cmd.exe")
	}
	return Spec{Program: "/bin/sh", Args: []string{"-c"}}
}

// Detect returns the shell to use on goos, probing the current host
// for a preferred interpreter and falling back to Default.
//
// Windows prefers PowerShell: models write PowerShell far more often
// than cmd, and cmd cannot glob, has its own expansion rules, and
// reports errors poorly. The order is PowerShell 7 (pwsh) →
// Windows PowerShell 5.1 → cmd.exe, each tried on PATH first and then
// at its well-known install location.
//
// goos is the OS the shell will run on (normally the deployment
// target); probing always happens against the current host, so a
// cross-host call simply finds nothing and returns Default.
func Detect(goos string) Spec {
	return detect(goos, hostProbe())
}

// pwshFallback and windowsPowerShellFallback are the documented install
// locations, used when PATH is curated (sandbox environment policies
// can strip entries) or the shell is simply not exported.
const (
	pwshFallback = `C:\Program Files\PowerShell\7\pwsh.exe`
	ps51Fallback = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
)

// windowsAppsDir marks the Store / AppExecutionAlias stubs. They are
// reparse points, and a low-integrity or elevated sandbox token cannot
// follow them, so a shell resolved there would look present and then
// fail to start.
const windowsAppsDir = `\windowsapps\`

// probe is the environment-facing half of detection, injectable so the
// Windows branch is testable from any host.
type probe struct {
	lookPath func(string) (string, error)
	getenv   func(string) string
	stat     func(string) error
}

func hostProbe() probe {
	return probe{
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
	}
}

func detect(goos string, p probe) Spec {
	if goos != "windows" {
		return Default(goos)
	}
	if program := resolveShell(p, "pwsh.exe", pwshFallback); program != "" {
		return powerShellSpec(program)
	}
	if program := resolveShell(p, "powershell.exe", ps51Path(p)); program != "" {
		return powerShellSpec(program)
	}
	if program := resolveShell(p, "cmd.exe", ""); program != "" {
		return cmdSpec(program)
	}
	return Default(goos)
}

// ps51Path is the System32 location of Windows PowerShell, built from
// the environment so a relocated Windows directory still resolves.
func ps51Path(p probe) string {
	root := strings.TrimSpace(p.getenv("SystemRoot"))
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(
		root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// resolveShell returns the first usable path for name: PATH first, then
// the well-known fallback.
func resolveShell(p probe, name, fallback string) string {
	if found, err := p.lookPath(name); err == nil && usableShellPath(found) {
		return found
	}
	if fallback != "" && usableShellPath(fallback) &&
		p.stat(fallback) == nil {
		return fallback
	}
	return ""
}

// usableShellPath rejects empty paths and Store aliases.
func usableShellPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return !strings.Contains(strings.ToLower(path), windowsAppsDir)
}

// powerShellSpec runs the script without the user profile: profiles
// change aliases and modules per machine, which would make an
// agent-authored command behave differently from the one approved.
func powerShellSpec(program string) Spec {
	return Spec{
		Program:   program,
		Args:      []string{"-NoProfile", "-Command"},
		SafeRunes: `\`,
	}
}

func cmdSpec(program string) Spec {
	return Spec{Program: program, Args: []string{"/c"}, SafeRunes: `\`}
}
