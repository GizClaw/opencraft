package sessions

import (
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"sigs.k8s.io/yaml"
)

// ThinkLevel is the per-session model reasoning effort. It mirrors
// flowcraft's inference.ReasoningEffort enum; the graph's
// ${board.think_level} reference consumes it verbatim.
type ThinkLevel string

const (
	// ThinkLow is the minimal reasoning effort.
	ThinkLow ThinkLevel = "low"
	// ThinkMedium is the default reasoning effort.
	ThinkMedium ThinkLevel = "medium"
	// ThinkHigh is the maximal reasoning effort.
	ThinkHigh ThinkLevel = "high"
)

// Valid reports whether the level is one of the supported values.
func (l ThinkLevel) Valid() bool {
	switch l {
	case ThinkLow, ThinkMedium, ThinkHigh:
		return true
	default:
		return false
	}
}

// thinkFile is the on-disk shape of <session>/think.yaml.
type thinkFile struct {
	Version string     `json:"version"`
	Level   ThinkLevel `json:"level"`
}

const thinkVersion = "v1"

// SetThink persists the reasoning effort for the session.
func (s *Store) SetThink(id string, level ThinkLevel) error {
	if !level.Valid() {
		return errdefs.Validationf(
			"sessions: unknown think level %q", level)
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(thinkFile{
		Version: thinkVersion,
		Level:   level,
	})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "think.yaml")
	tmp, err := os.CreateTemp(dir, ".think-*")
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
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Think returns the persisted reasoning effort for the session,
// defaulting to medium when the session has no think file.
func (s *Store) Think(id string) (ThinkLevel, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(id), "think.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return ThinkMedium, nil
		}
		return ThinkMedium, err
	}
	var f thinkFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return ThinkMedium, err
	}
	if !f.Level.Valid() {
		return ThinkMedium, nil
	}
	return f.Level, nil
}
