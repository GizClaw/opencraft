package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// maxSessionImportBundleBytes bounds one imported session bundle. A
// session transcript can be large, but the file is read fully into
// memory, so the host still needs an upper bound.
const maxSessionImportBundleBytes = 128 << 20 // 128 MiB

// wirePluginSessionImport routes the capability-plugin session.import
// primitive into the shared Host. It is the desktopv2 replacement for
// the old App.handleSessionImport wiring and is permission-gated: a
// capability plugin must declare sessions:import in its manifest.
func (c *Core) wirePluginSessionImport() {
	if c.Plugin == nil || c.Plugin.Capability == nil {
		return
	}
	c.Plugin.Capability.SetSessionImportHandler(pluginruntime.SessionImportHandler{
		Import: c.handlePluginSessionImport,
	})
}

// wirePluginWorkspace answers the workspace.current primitive with the
// active workspace path. Capability subprocesses are long-lived and do
// not observe environment-variable changes across workspace switches,
// so the answer is read dynamically on every call.
func (c *Core) wirePluginWorkspace() {
	if c.Plugin == nil || c.Plugin.Capability == nil {
		return
	}
	c.Plugin.Capability.SetWorkspaceHandler(pluginruntime.WorkspaceHandler{
		Current: func() (string, error) {
			return c.ActiveWorkDir(), nil
		},
	})
}

// handlePluginSessionImport imports one plugin-written session bundle
// into the target workspace. The bundle is a neutral JSON transcript
// whose Source key dedupes repeated imports; history and memory are
// seeded by the shared Host so the imported session can be continued.
func (c *Core) handlePluginSessionImport(
	pluginID string,
	req pluginruntime.SessionImportRequest,
) (pluginruntime.SessionImportResult, error) {
	if !c.pluginHasPermission(pluginID, "sessions:import") {
		return pluginruntime.SessionImportResult{}, fmt.Errorf(
			"session.import: plugin %q lacks sessions:import permission",
			pluginID)
	}
	ctx := c.Shell.Context()
	workDir, err := c.pluginImportWorkDir(req.Workspace)
	if err != nil {
		return pluginruntime.SessionImportResult{}, err
	}
	h, err := c.hostForPluginImport(ctx, workDir)
	if err != nil {
		return pluginruntime.SessionImportResult{}, err
	}

	bundle, err := readSessionImportBundle(req.BundlePath)
	if err != nil {
		return pluginruntime.SessionImportResult{}, err
	}
	if strings.TrimSpace(req.Source) != "" {
		bundle.Source = strings.TrimSpace(req.Source)
	}
	if strings.TrimSpace(req.Title) != "" {
		bundle.Title = strings.TrimSpace(req.Title)
	}
	messages, turns := countSessionImportBundle(bundle)

	id, err := h.ImportSession(ctx, bundle)
	if err != nil {
		return pluginruntime.SessionImportResult{}, err
	}
	if h == c.Runtime.Current() {
		c.Shell.Emit("session_updated", map[string]string{"id": id})
	}
	return pluginruntime.SessionImportResult{
		SessionID: id,
		Messages:  messages,
		Turns:     turns,
	}, nil
}

// pluginImportWorkDir resolves the session.import target: the
// active workspace by default, or one previously opened workspace
// selected by path. Unknown workspaces are rejected so a plugin cannot
// fabricate a data root.
func (c *Core) pluginImportWorkDir(workspace string) (string, error) {
	active := strings.TrimSpace(c.ActiveWorkDir())
	wd := strings.TrimSpace(workspace)
	if wd == "" {
		wd = active
	}
	if wd == "" {
		return "", errors.New("session.import: no workspace selected")
	}
	wd = filepath.Clean(wd)
	if filepath.Clean(active) != wd {
		metas, err := c.Workspaces()
		if err != nil {
			return "", fmt.Errorf("session.import: list workspaces: %w", err)
		}
		for _, m := range metas {
			if filepath.Clean(m.Path) == wd {
				return wd, nil
			}
		}
		return "", fmt.Errorf(
			"session.import: workspace %s has not been opened", wd)
	}
	return wd, nil
}

// hostForPluginImport returns the shared Host for workDir, reusing the
// currently active Host when it matches. Assembling a Host for a
// background workspace is the same cost automation turns already pay.
func (c *Core) hostForPluginImport(
	ctx context.Context, workDir string,
) (*host.Host, error) {
	if h := c.Runtime.Current(); h != nil &&
		filepath.Clean(h.WorkDir()) == filepath.Clean(workDir) {
		return h, nil
	}
	h, err := c.Runtime.AcquireBackground(ctx, workDir, interact.Auto{})
	if err != nil {
		return nil, fmt.Errorf("session.import: workspace runtime: %w", err)
	}
	return h, nil
}

// pluginHasPermission reports whether one installed plugin's manifest
// declares perm. Capability primitives are otherwise unauthenticated,
// so the host write path checks the declaring permission itself.
func (c *Core) pluginHasPermission(pluginID, perm string) bool {
	if c.Plugin == nil || c.Plugin.Store == nil ||
		strings.TrimSpace(pluginID) == "" {
		return false
	}
	m, err := c.Plugin.Store.Manifest(pluginID)
	if err != nil {
		return false
	}
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

func readSessionImportBundle(path string) (ocsessions.ImportRequest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: bundle: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: bundle is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: read bundle: %w", err)
	}
	if int64(len(data)) > maxSessionImportBundleBytes {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: bundle exceeds %d bytes",
			maxSessionImportBundleBytes)
	}
	var req ocsessions.ImportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: decode bundle: %w", err)
	}
	return req, nil
}

func countSessionImportBundle(req ocsessions.ImportRequest) (messages, turns int) {
	turns = len(req.Turns)
	for _, turn := range req.Turns {
		messages += len(turn.Messages)
	}
	return messages, turns
}
