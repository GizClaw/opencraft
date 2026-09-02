//go:build windows

package toolchain

import (
	"os"
	"os/exec"
)

// execTool spawns the resolved tool and mirrors stdio/exit status.
// Windows has no execve replacement, so the launcher stays as an
// intermediate process.
func execTool(path, _ string, args []string, env []string) error {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
