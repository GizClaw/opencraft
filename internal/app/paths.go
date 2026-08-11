package app

import (
	"os"
	"path/filepath"
)

// UserDataDir returns ~/.opencraft, creating it if needed. It holds user
// data (SQLite DB, rollouts, logs).
func UserDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".opencraft")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// UserConfigDir returns ~/.opencraft/config, creating it if needed. It
// holds the user-facing configuration files (currently inference.yaml).
func UserConfigDir() (string, error) {
	data, err := UserDataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(data, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
