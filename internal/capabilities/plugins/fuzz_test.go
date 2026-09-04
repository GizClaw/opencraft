package plugins

import (
	"os"
	"testing"
)

func FuzzExtractPluginZip(f *testing.F) {
	f.Add([]byte("PK\x03\x04garbage"))
	f.Add([]byte("not a zip"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Write the bytes to a temp zip and run the extraction guard.
		path := t.TempDir() + "/p.zip"
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return
		}
		_, cleanup, err := extractPluginZip(path)
		if err == nil {
			cleanup()
		}
	})
}
