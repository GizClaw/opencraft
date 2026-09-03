package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// maxSessionImportBundleBytes bounds one imported session bundle. A
// session transcript can be large, but the file is read fully into
// memory, so the host still needs an upper bound.
const maxSessionImportBundleBytes = 128 << 20 // 128 MiB

// ImportSessionBundle is the Wails binding counterpart to the per-row
// export action: it lets the UI pick an OpenCraft session bundle and
// import it into the currently open workspace.
func (a *App) ImportSessionBundle(path string) (SessionImportDTO, error) {
	res, err := a.importSessionBundle(pluginruntime.SessionImportRequest{
		BundlePath: path,
	})
	if err != nil {
		return SessionImportDTO{}, err
	}
	return SessionImportDTO{
		SessionID: res.SessionID,
		Messages:  res.Messages,
		Turns:     res.Turns,
	}, nil
}

// ExportSessionBundle writes the same neutral JSON bundle that
// ImportSessionBundle consumes, so a session can be moved between
// workspaces (or saved for a later import). The existing ExportSession
// keeps writing the human-readable Markdown transcript.
func (a *App) ExportSessionBundle(id string) (string, error) {
	if !ocsessions.ValidID(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	store := a.sessionStore()
	workDir := a.snapshotWorkDir()
	if store == nil || strings.TrimSpace(workDir) == "" {
		return "", errors.New("session store is not available")
	}
	turns, err := store.Turns(a.appContext(), id)
	if err != nil {
		return "", err
	}
	if len(turns) == 0 {
		return "", errors.New("session has no archived turns")
	}

	bundle := ocsessions.ImportRequest{
		Source: "opencraft:" + id,
		Title:  a.sessionTitle(store, id, id),
		Turns:  make([]ocsessions.ImportTurn, 0, len(turns)),
	}
	for _, turn := range turns {
		at := turn.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		bundle.Turns = append(bundle.Turns, ocsessions.ImportTurn{
			At:       at,
			Messages: turn.Messages,
		})
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	dir := a.exportsDirFor(workDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// handleSessionImport is registered as the runtime's plugin→host
// primitive. It is permission-gated so a capability plugin must
// declare sessions:import in its manifest.
func (a *App) handleSessionImport(
	pluginID string,
	req pluginruntime.SessionImportRequest,
) (pluginruntime.SessionImportResult, error) {
	if !a.sessionImportPermitted(pluginID) {
		return pluginruntime.SessionImportResult{}, fmt.Errorf(
			"session.import: plugin %q lacks sessions:import permission",
			pluginID)
	}
	res, err := a.importSessionBundle(req)
	if err == nil && a.bridge != nil {
		// The plugin call is not routed through the UI binding, so the
		// frontend needs an explicit event to refresh the session list.
		a.bridge.Emit("session_updated", map[string]string{"id": res.SessionID})
	}
	return res, err
}

func (a *App) sessionImportPermitted(pluginID string) bool {
	if a.plugins == nil || pluginID == "" {
		return false
	}
	m, err := a.plugins.Manifest(pluginID)
	if err != nil {
		return false
	}
	for _, p := range m.Permissions {
		if p == "sessions:import" {
			return true
		}
	}
	return false
}

func (a *App) importSessionBundle(
	req pluginruntime.SessionImportRequest,
) (pluginruntime.SessionImportResult, error) {
	if strings.TrimSpace(req.BundlePath) == "" {
		return pluginruntime.SessionImportResult{},
			errors.New("session.import: bundle_path is required")
	}

	a.sessionImportMu.Lock()
	defer a.sessionImportMu.Unlock()

	store, assembly, release, err := a.resolveSessionImportTarget(req.Workspace)
	if err != nil {
		return pluginruntime.SessionImportResult{}, err
	}
	defer release()
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
	messages, turns := countImportBundle(bundle)

	ctx := a.appContext()
	id, err := store.Import(ctx, bundle)
	if err != nil {
		return pluginruntime.SessionImportResult{}, err
	}
	ready, err := store.ImportReady(ctx, id)
	if err != nil {
		_ = store.AbortImport(ctx, id)
		return pluginruntime.SessionImportResult{}, err
	}
	if !ready {
		flat := flattenImportTurns(bundle)
		if err := opmemory.SeedConversation(
			ctx, assembly, id, bundle.Source, flat,
		); err != nil {
			rollbackErr := store.AbortImport(ctx, id)
			return pluginruntime.SessionImportResult{},
				errors.Join(fmt.Errorf("session.import: seed memory: %w", err),
					rollbackErr)
		}
		if err := store.CompleteImport(ctx, id); err != nil {
			rollbackErr := store.AbortImport(ctx, id)
			return pluginruntime.SessionImportResult{},
				errors.Join(fmt.Errorf("session.import: complete: %w", err),
					rollbackErr)
		}
	}
	return pluginruntime.SessionImportResult{
		SessionID: id,
		Messages:  messages,
		Turns:     turns,
	}, nil
}

func (a *App) resolveSessionImportTarget(
	workspace string,
) (*ocsessions.Store, memory.TurnSink, func(), error) {
	currentWD := a.snapshotWorkDir()

	wd := strings.TrimSpace(workspace)
	if wd == "" {
		wd = currentWD
	}
	if wd == "" {
		return nil, nil, nil, errors.New(
			"session.import: no workspace selected")
	}

	var h *host.Host
	var release func()
	if filepath.Clean(wd) != filepath.Clean(currentWD) {
		if !a.isKnownWorkspace(wd) {
			return nil, nil, nil, fmt.Errorf(
				"session.import: workspace %s has not been opened", wd)
		}
		var err error
		h, release, err = a.hostForRun(wd)
		if err != nil {
			return nil, nil, nil, fmt.Errorf(
				"session.import: workspace runtime: %w", err)
		}
	} else {
		a.mu.Lock()
		h = a.currentHost
		a.mu.Unlock()
		release = func() {}
	}
	if h == nil || h.Sessions() == nil || h.Controller() == nil ||
		h.Controller().Runtime() == nil {
		release()
		return nil, nil, nil, errors.New(
			"session.import: runtime is not ready")
	}
	value, ok := h.Controller().Runtime().Resource("mem")
	if !ok {
		release()
		return nil, nil, nil, errors.New(
			"session.import: memory assembly is not configured")
	}
	assembly, ok := value.(memory.TurnSink)
	if !ok || assembly == nil {
		release()
		return nil, nil, nil, fmt.Errorf(
			"session.import: memory resource has type %T", value)
	}
	return h.Sessions(), assembly, release, nil
}

func (a *App) isKnownWorkspace(path string) bool {
	dir, err := workspaceHistoryDir()
	if err != nil {
		return false
	}
	metas, err := loadWorkspaces(dir)
	if err != nil {
		return false
	}
	want := filepath.Clean(path)
	for _, m := range metas {
		if filepath.Clean(m.Path) == want {
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
	if info.Size() > maxSessionImportBundleBytes {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: bundle exceeds %d bytes",
			maxSessionImportBundleBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return ocsessions.ImportRequest{}, fmt.Errorf(
			"session.import: read bundle: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxSessionImportBundleBytes+1))
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

func countImportBundle(req ocsessions.ImportRequest) (messages, turns int) {
	turns = len(req.Turns)
	for _, turn := range req.Turns {
		messages += len(turn.Messages)
	}
	return messages, turns
}

func flattenImportTurns(req ocsessions.ImportRequest) []message.Message {
	var out []message.Message
	for _, turn := range req.Turns {
		out = append(out, turn.Messages...)
	}
	return out
}
