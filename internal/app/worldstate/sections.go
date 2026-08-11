package worldstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/sdk/workspace"
)

const (
	agentsSeparator  = "\n\n--- project-doc ---\n\n"
	defaultDocBudget = 16 << 10 // 16 KiB
)

func (s *Service) agentsSection() (Section, error) {
	text, err := s.discoverAgents()
	if err != nil {
		return Section{}, err
	}
	return Section{ID: "agents_md", Role: "user", Text: text}, nil
}

func (s *Service) permissionsSection() (Section, error) {
	text, err := render(permissionsTmpl, permissionsData{
		Profile:          s.opts.PermissionProfile,
		ApprovedPrefixes: strings.Join(s.opts.ApprovedPrefixes, ", "),
	})
	if err != nil {
		return Section{}, err
	}
	return Section{ID: "permissions", Role: "system", Text: text}, nil
}

func (s *Service) environmentSection() (Section, error) {
	text, err := render(environmentTmpl, environmentData{
		WorkspaceRoot:     s.opts.WorkBase,
		CollaborationMode: s.opts.CollaborationMode,
	})
	if err != nil {
		return Section{}, err
	}
	return Section{ID: "environment", Role: "system", Text: text}, nil
}

// discoverAgents collects AGENTS.md from the project root down to the
// working directory (per-directory AGENTS.override.md wins), appends the
// user-level ~/.opencraft/AGENTS.md, and applies the doc budget.
func (s *Service) discoverAgents() (string, error) {
	root := projectRoot(s.opts.WorkBase)
	var dirs []string
	for dir := s.opts.WorkBase; ; {
		dirs = append(dirs, dir)
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// root -> cwd order
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	var entries []string
	for _, dir := range dirs {
		if text, err := s.readDoc(dir, "AGENTS.override.md"); err != nil {
			return "", err
		} else if text != "" {
			entries = append(entries, text)
			continue
		}
		if text, err := s.readDoc(dir, "AGENTS.md"); err != nil {
			return "", err
		} else if text != "" {
			entries = append(entries, text)
		}
	}
	if s.opts.UserDir != "" {
		if text := readHostDoc(filepath.Join(s.opts.UserDir, "AGENTS.md")); text != "" {
			entries = append(entries, text)
		}
	}
	if len(entries) == 0 {
		return "", nil
	}
	return truncateBytes(strings.Join(entries, agentsSeparator), defaultDocBudget), nil
}

// readDoc reads one AGENTS doc from dir. Paths inside the workspace root
// go through the workspace interface; anything else (ancestor dirs, user
// config) uses the host filesystem.
func (s *Service) readDoc(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	if s.opts.Workspace != nil && dir == s.opts.WorkBase {
		data, err := s.opts.Workspace.Read(context.Background(), name)
		if err != nil {
			if errors.Is(err, workspace.ErrNotFound) || os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return readHostDoc(path), nil
}

// projectRoot walks upward from dir to the nearest directory containing
// a .git marker. Falls back to dir itself.
func projectRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

func readHostDoc(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
