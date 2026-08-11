// Package applypatch implements the codex-style apply_patch tool: a
// minimal line-based patch format applied through sdk/workspace.
package applypatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/sdk/errdefs"
	"github.com/GizClaw/flowcraft/sdk/workspace"
)

type opKind int

const (
	opAdd opKind = iota
	opUpdate
	opDelete
)

type op struct {
	kind  opKind
	path  string
	body  []string // add: content lines
	hunks []hunk   // update
}

type hunk struct {
	anchor  string
	removed []string
	added   []string
}

// Parse parses the codex apply_patch format:
//
//	*** Begin Patch
//	*** Add File: path
//	+line
//	*** Update File: path
//	@@ context
//	-old
//	+new
//	*** Delete File: path
//	*** End Patch
func Parse(patch string) ([]*op, error) {
	lines := strings.Split(patch, "\n")
	var ops []*op
	var current *op
	inPatch := false

	for i := range lines {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "*** Begin Patch":
			if inPatch {
				return nil, errdefs.Validationf("apply_patch: duplicate Begin Patch")
			}
			inPatch = true
		case trimmed == "*** End Patch":
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: End Patch without Begin")
			}
			if current != nil && current.kind == opUpdate && len(current.hunks) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: update %q has no hunks", current.path)
			}
			return ops, nil
		case strings.HasPrefix(trimmed, "*** Add File: "):
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: Add File outside patch")
			}
			if current != nil && current.kind == opAdd && len(current.body) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: add %q has no lines", current.path)
			}
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File: "))
			if err := validatePath(p); err != nil {
				return nil, err
			}
			current = &op{kind: opAdd, path: p}
			ops = append(ops, current)
		case strings.HasPrefix(trimmed, "*** Update File: "):
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: Update File outside patch")
			}
			if current != nil && current.kind == opUpdate && len(current.hunks) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: update %q has no hunks", current.path)
			}
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File: "))
			if err := validatePath(p); err != nil {
				return nil, err
			}
			current = &op{kind: opUpdate, path: p}
			ops = append(ops, current)
		case strings.HasPrefix(trimmed, "*** Delete File: "):
			if !inPatch {
				return nil, errdefs.Validationf("apply_patch: Delete File outside patch")
			}
			if current != nil && current.kind == opAdd && len(current.body) == 0 {
				return nil, errdefs.Validationf(
					"apply_patch: add %q has no lines", current.path)
			}
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Delete File: "))
			if err := validatePath(p); err != nil {
				return nil, err
			}
			current = &op{kind: opDelete, path: p}
			ops = append(ops, current)
		case !inPatch:
			return nil, errdefs.Validationf(
				"apply_patch: unexpected line outside patch: %q", line)
		default:
			if current == nil {
				return nil, errdefs.Validationf(
					"apply_patch: unexpected line before any file op: %q", line)
			}
			if current.kind == opAdd {
				if !strings.HasPrefix(line, "+") {
					return nil, errdefs.Validationf(
						"apply_patch: add line for %q must start with '+': %q",
						current.path, line)
				}
				current.body = append(current.body, strings.TrimPrefix(line, "+"))
				continue
			}
			if current.kind == opUpdate {
				switch {
				case strings.HasPrefix(line, "@@"):
					current.hunks = append(current.hunks, hunk{
						anchor: strings.TrimSpace(strings.TrimPrefix(line, "@@")),
					})
				case len(current.hunks) == 0:
					return nil, errdefs.Validationf(
						"apply_patch: update %q line before first @@: %q",
						current.path, line)
				case strings.HasPrefix(line, "-"):
					h := &current.hunks[len(current.hunks)-1]
					h.removed = append(h.removed, strings.TrimPrefix(line, "-"))
				case strings.HasPrefix(line, "+"):
					h := &current.hunks[len(current.hunks)-1]
					h.added = append(h.added, strings.TrimPrefix(line, "+"))
				case strings.HasPrefix(line, " "):
					h := &current.hunks[len(current.hunks)-1]
					content := strings.TrimPrefix(line, " ")
					h.removed = append(h.removed, content)
					h.added = append(h.added, content)
				case line == "":
					// trailing blank inside a hunk: ignore
				default:
					return nil, errdefs.Validationf(
						"apply_patch: update %q unexpected line: %q",
						current.path, line)
				}
				continue
			}
			// delete op: ignore stray blank lines
			if line != "" {
				return nil, errdefs.Validationf(
					"apply_patch: delete %q must not have a body: %q",
					current.path, line)
			}
		}
	}
	return nil, errdefs.Validationf("apply_patch: missing End Patch marker")
}

func validatePath(path string) error {
	if path == "" {
		return errdefs.Validationf("apply_patch: empty file path")
	}
	if filepath.IsAbs(path) {
		return errdefs.Validationf(
			"apply_patch: absolute path %q is not allowed", path)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return errdefs.Validationf(
			"apply_patch: path %q escapes the workspace", path)
	}
	return nil
}

// Apply applies parsed ops to a workspace.
func Apply(ctx context.Context, ws workspace.Workspace, ops []*op) ([]FileResult, error) {
	var results []FileResult
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.kind {
		case opAdd:
			exists, err := ws.Exists(ctx, op.path)
			if err != nil {
				return results, err
			}
			if exists {
				return results, errdefs.Conflictf(
					"apply_patch: file %q already exists", op.path)
			}
			content := strings.Join(op.body, "\n") + "\n"
			if err := ws.Write(ctx, op.path, []byte(content)); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "add"})
		case opUpdate:
			if err := applyUpdate(ctx, ws, op); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "update"})
		case opDelete:
			exists, err := ws.Exists(ctx, op.path)
			if err != nil {
				return results, err
			}
			if !exists {
				return results, errdefs.NotFoundf(
					"apply_patch: file %q does not exist", op.path)
			}
			if err := ws.Delete(ctx, op.path); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "delete"})
		}
	}
	return results, nil
}

func applyUpdate(ctx context.Context, ws workspace.Workspace, op *op) error {
	data, err := ws.Read(ctx, op.path)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || os.IsNotExist(err) {
			return errdefs.NotFoundf(
				"apply_patch: file %q does not exist", op.path)
		}
		return err
	}
	lines := splitLines(string(data))
	for _, h := range op.hunks {
		next, found := applyHunk(lines, h)
		if !found {
			return errdefs.Validationf(
				"apply_patch: hunk in %q did not match (anchor %q)",
				op.path, h.anchor)
		}
		lines = next
	}
	return ws.Write(ctx, op.path, []byte(strings.Join(lines, "\n")+"\n"))
}

// applyHunk replaces the first match of removed lines with added lines.
// Insertion hunks (no removed lines) use the @@ anchor to find position.
func applyHunk(lines []string, h hunk) ([]string, bool) {
	if len(h.removed) == 0 {
		idx := -1
		if h.anchor != "" {
			for i, line := range lines {
				if strings.Contains(line, h.anchor) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			return lines, false
		}
		out := make([]string, 0, len(lines)+len(h.added))
		out = append(out, lines[:idx+1]...)
		out = append(out, h.added...)
		out = append(out, lines[idx+1:]...)
		return out, true
	}
	for i := 0; i+len(h.removed) <= len(lines); i++ {
		match := true
		for j := range h.removed {
			if lines[i+j] != h.removed[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		out := make([]string, 0, len(lines)-len(h.removed)+len(h.added))
		out = append(out, lines[:i]...)
		out = append(out, h.added...)
		out = append(out, lines[i+len(h.removed):]...)
		return out, true
	}
	return lines, false
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// FileResult describes one applied file operation.
type FileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}
