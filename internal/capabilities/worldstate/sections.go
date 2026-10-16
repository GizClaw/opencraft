package worldstate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/workspace"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/tools/plan"
)

const (
	agentsSeparator  = "\n\n--- project-doc ---\n\n"
	defaultDocBudget = 16 << 10 // 16 KiB
	// maxDocReadBytes is how much of an AGENTS doc is read at all. A
	// doc at or below defaultDocBudget is used whole, and one above it
	// is truncated to the budget after the read, so the budget stays
	// the only thing the model sees. This is the ceiling on what the
	// read may allocate: a doc past it is skipped with a warning rather
	// than carried into memory to keep the first 16 KiB of it.
	maxDocReadBytes = 1 << 20 // 1 MiB
)

// oversizedDocMessage names the limit once, in the message the skip
// logs, so the number cannot drift between the check and the report.
var oversizedDocMessage = fmt.Sprintf(
	"worldstate: AGENTS doc skipped: larger than %d bytes", maxDocReadBytes)

// warnOversizedDoc records the one skip a reader would otherwise have
// to guess at: the doc is there, and the model does not see it.
func warnOversizedDoc(ctx context.Context, path string, size int64) {
	attrs := []attribute.KeyValue{attribute.String("path", path)}
	if size >= 0 {
		attrs = append(attrs, attribute.Int64("bytes", size))
	}
	telemetry.Warn(ctx, oversizedDocMessage, attrs...)
}

func (s *Service) agentsSection(ctx context.Context) (Section, error) {
	text, err := s.discoverAgents(ctx)
	if err != nil {
		return Section{}, err
	}
	return newTextSection("agents_md", message.RoleUser, text), nil
}

func (s *Service) permissionsSection(
	ctx context.Context, contextID string,
) (Section, error) {
	var prefixes []string
	if s.prefixes != nil {
		prefixes = s.prefixes.Rules()
	}
	yolo := false
	readOnly := false
	if s.sessionStore != nil {
		if mode, err := s.sessionStore.Mode(ctx, contextID); err == nil {
			yolo = mode.IsYOLO()
			readOnly = mode.IsReadOnly()
		}
	}
	text, err := render(permissionsTmpl, permissionsData{
		Profile:          s.opts.PermissionProfile,
		ApprovedPrefixes: strings.Join(prefixes, ", "),
		YOLO:             yolo,
		ReadOnly:         readOnly,
	})
	if err != nil {
		return Section{}, err
	}
	return newTextSection("permissions", message.RoleSystem, text), nil
}

func (s *Service) environmentSection() (Section, error) {
	text, err := render(environmentTmpl, environmentData{
		WorkspaceRoot:     s.opts.WorkBase,
		CollaborationMode: s.opts.CollaborationMode,
	})
	if err != nil {
		return Section{}, err
	}
	return newTextSection("environment", message.RoleSystem, text), nil
}

// renderPlanSection formats the latest plan for the world state as a
// checklist so the model sees its TODO list with statuses.
func renderPlanSection(p plan.Plan) string {
	items := make([]planItemData, 0, len(p.Items))
	for _, item := range p.Items {
		marker := "[ ]"
		switch item.Status {
		case plan.StatusCompleted:
			marker = "[x]"
		case plan.StatusInProgress:
			marker = "[~]"
		}
		items = append(items, planItemData{
			Step:   item.Step,
			Marker: marker,
			Status: item.Status,
		})
	}
	out, err := render(planTmpl, planData{
		Items:       items,
		Explanation: p.Explanation,
	})
	if err != nil {
		return ""
	}
	return strings.TrimRight(out, "\n")
}

// discoverAgents collects AGENTS.md from the project root down to the
// working directory (per-directory AGENTS.override.md wins), appends the
// user-level ~/.opencraft/AGENTS.md, and applies the doc budget.
func (s *Service) discoverAgents(ctx context.Context) (string, error) {
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
		if text, err := s.readDoc(ctx, dir, "AGENTS.override.md"); err != nil {
			return "", err
		} else if text != "" {
			entries = append(entries, text)
			continue
		}
		if text, err := s.readDoc(ctx, dir, "AGENTS.md"); err != nil {
			return "", err
		} else if text != "" {
			entries = append(entries, text)
		}
	}
	if s.opts.UserDir != "" {
		if text := readHostDoc(ctx, filepath.Join(s.opts.UserDir, "AGENTS.md")); text != "" {
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
//
// Both paths answer the same way about the file itself: anything that is
// not a regular file counts as no doc. They used to disagree - a
// directory named AGENTS.md beside the working directory failed the
// whole world-state build (the workspace read reported it as an error,
// not as "missing"), while the same name one directory up read as
// empty. A doc is context, and context that exists in the wrong shape
// is not worth a failed turn.
//
// What they still do not share is whose error it is: a workspace read
// that fails for another reason is returned (the workspace is the app's
// own tree, and a refusal there is worth reporting), while an ancestor
// path outside it stays best-effort.
func (s *Service) readDoc(ctx context.Context, dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	if s.opts.Workspace != nil && dir == s.opts.WorkBase {
		data, err := s.readWorkspaceDoc(ctx, name)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return readHostDoc(ctx, path), nil
}

// readWorkspaceDoc reads one doc through the workspace interface,
// bounded by maxDocReadBytes. The bound matters because the doc is
// truncated to defaultDocBudget afterwards: reading a huge file to keep
// its first 16 KiB allocates the whole thing first.
func (s *Service) readWorkspaceDoc(ctx context.Context, name string) ([]byte, error) {
	info, err := s.opts.Workspace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, nil
	}
	if info.Size() > maxDocReadBytes {
		warnOversizedDoc(ctx, name, info.Size())
		return nil, nil
	}
	// ReadLimited is preferred over Read where the workspace has it:
	// the stat above bounds what is read, and the bounded read covers a
	// file that grows between the two calls.
	if limited, ok := s.opts.Workspace.(workspace.LimitedReader); ok {
		data, err := limited.ReadLimited(ctx, name, maxDocReadBytes)
		switch {
		case err == nil:
			return data, nil
		case errors.Is(err, workspace.ErrNotFound) || os.IsNotExist(err):
			return nil, nil
		case errdefs.IsValidation(err):
			// The file outgrew the stat's answer: same rule as above.
			warnOversizedDoc(ctx, name, -1)
			return nil, nil
		default:
			return nil, err
		}
	}
	data, err := s.opts.Workspace.Read(ctx, name)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return []byte(truncateBytes(string(data), maxDocReadBytes)), nil
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

// readHostDoc reads one doc from the host filesystem, applying the same
// rules as the workspace path: not a regular file, or bigger than
// maxDocReadBytes, is not a doc. Errors stay silent here (an ancestor
// directory outside the workspace is not the app's to fail on), but the
// oversize case is warned about, because skipping a doc the user wrote
// is worth a line in the log.
func readHostDoc(ctx context.Context, path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if info.Size() > maxDocReadBytes {
		warnOversizedDoc(ctx, path, info.Size())
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			telemetry.WarnErr(ctx, "worldstate: close doc failed", cerr)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, maxDocReadBytes+1))
	if err != nil || int64(len(data)) > maxDocReadBytes {
		if err == nil {
			warnOversizedDoc(ctx, path, -1)
		}
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
