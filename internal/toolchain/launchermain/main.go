// Command launchermain is the executable backing <runtime>/launcher
// symlinks. Release packaging builds this binary once and links the
// tool names (python, python3, node, npm, npx, corepack, uv, uvx) to
// it; the tool to launch is derived from argv[0].
package main

import (
	"os"

	"github.com/GizClaw/opencraft/internal/toolchain"
)

func main() {
	os.Exit(toolchain.LauncherMain(os.Args))
}
