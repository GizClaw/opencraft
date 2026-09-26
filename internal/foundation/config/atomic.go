package config

import (
	"os"

	"github.com/GizClaw/opencraft/internal/foundation/utils/fsatomic"
)

// writeFileAtomic writes data to path via a temp file in the same
// directory followed by rename, so a crash mid-write never leaves a
// truncated user configuration file. The final file inherits perm
// (0600 for the secrets-bearing opencraft.yaml). The mechanics live in
// fsatomic; this wrapper only pins the options config has always used
// (create the directory, fsync before the rename).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	return fsatomic.Write(path, data, fsatomic.Options{
		Perm:       perm,
		MkdirPerm:  0o700,
		TempPrefix: ".opencraft-*.tmp",
		Sync:       true,
	})
}
