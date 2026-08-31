//go:build windows

package execd

// defaultShell reports the conventional Windows shell. Informational
// only; the agent decides the actual command interpreter.
func defaultShell() string { return "cmd.exe" }
