//go:build !darwin && !linux

package config

import (
	_ "embed"
	"strings"
)

//go:embed assets/sandbox_other.yaml
var sandboxTemplate []byte

func platformSandbox(cacheDir string) []byte {
	return []byte(strings.ReplaceAll(
		string(sandboxTemplate), "${CACHE_DIR}", cacheDir))
}
