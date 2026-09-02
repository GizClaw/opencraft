//go:build !windows

package toolchain

import "syscall"

// execTool replaces the current process with the resolved tool, so
// sandbox process groups and signals keep working exactly as if the
// tool had been spawned directly.
func execTool(path, tool string, args []string, env []string) error {
	argv := append([]string{tool}, args...)
	return syscall.Exec(path, argv, env)
}
