package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteState atomically persists a JSON document as
// <session>/<name>.json. It is the single file-I/O owner for
// per-session JSON state (plans, and future session-scoped docs), so
// consumers never reimplement atomic writes or path handling.
func (s *Store) WriteState(id, name string, v any) error {
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name+".json")
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
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

// ReadState loads a JSON document from <session>/<name>.json. A
// missing file returns an os.ErrNotExist error.
func (s *Store) ReadState(id, name string, v any) error {
	data, err := os.ReadFile(filepath.Join(s.dir(id), name+".json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
