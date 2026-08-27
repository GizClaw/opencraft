package sessions

import (
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"sigs.k8s.io/yaml"
)

// Mode is the per-session sandbox permission mode.
type Mode string

const (
	// ModeWorkspace confines commands and files to the workspace and
	// asks the user before running unlisted commands (the default).
	ModeWorkspace Mode = "workspace"
	// ModeReadOnly keeps the workspace root read-only for every command
	// (OS-enforced by the seatbelt/bwrap backend); known read-only
	// commands run without approval, everything else asks the user.
	ModeReadOnly Mode = "read-only"
	// ModeYOLO disables the sandbox for the session: commands run on
	// the host with the full environment, no approval prompts, and
	// file tools may reach any path.
	ModeYOLO Mode = "yolo"
)

// IsYOLO reports whether m is the unconfined mode.
func (m Mode) IsYOLO() bool { return m == ModeYOLO }

// IsReadOnly reports whether m keeps the workspace root read-only.
func (m Mode) IsReadOnly() bool { return m == ModeReadOnly }

// modeFile is the on-disk shape of <session>/permissions.yaml.
type modeFile struct {
	Version string `json:"version"`
	Mode    Mode   `json:"mode"`
}

const modeVersion = "v1"

// SetMode persists the sandbox mode for the session.
func (s *Store) SetMode(id string, mode Mode) error {
	switch mode {
	case ModeWorkspace, ModeReadOnly, ModeYOLO:
	default:
		return errdefs.Validationf(
			"sessions: unknown permission mode %q", mode)
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(modeFile{
		Version: modeVersion,
		Mode:    mode,
	})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "permissions.yaml")
	tmp, err := os.CreateTemp(dir, ".permissions-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Mode returns the persisted sandbox mode for the session, defaulting
// to workspace when the session has no permissions file.
func (s *Store) Mode(id string) (Mode, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(id), "permissions.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return ModeWorkspace, nil
		}
		return ModeWorkspace, err
	}
	var f modeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return ModeWorkspace, err
	}
	switch f.Mode {
	case ModeWorkspace, ModeReadOnly, ModeYOLO:
		return f.Mode, nil
	default:
		return ModeWorkspace, nil
	}
}
