//go:build !windows

package execd

// defaultShell is the shell name reported by EnvironmentInfo. The field
// is informational (the agent decides the actual command interpreter),
// so each platform reports its conventional shell.
func defaultShell() string { return "/bin/sh" }
