package desktop

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	otellog "go.opentelemetry.io/otel/log"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/capabilities/undo"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// Version returns the application version.
func (a *App) Version() string {
	return octelemetry.ServiceVersion
}

// ConfigStatus returns the current configuration state; the frontend
// pulls it on mount and after every rebuild.
func (a *App) ConfigStatus() (ConfigStatus, error) {
	configured, err := a.inferenceConfigured()
	if err != nil {
		return ConfigStatus{}, err
	}
	return a.status(configured), nil
}

// ModelCatalog returns every driver's built-in model catalog for the
// settings page dropdown.
func (a *App) ModelCatalog() ([]config.ProviderModels, error) {
	return config.ModelCatalog()
}

// Providers returns the provider catalog.
func (a *App) Providers() []ProviderView {
	out := make([]ProviderView, 0, len(config.Providers))
	for _, p := range config.Providers {
		out = append(out, ProviderView{
			ID:            p.ID,
			Name:          p.Name,
			DefaultModel:  p.DefaultModel,
			EnvVar:        p.EnvVar,
			API:           p.API,
			Azure:         p.Azure,
			ModelEndpoint: p.ModelEndpoint,
		})
	}
	return out
}

// ConfigState returns the configured inference wiring (providers in
// router priority order with their keys/models/capabilities) plus the
// current default model, so the config page can edit in place.
func (a *App) ConfigState() (ConfigState, error) {
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return ConfigState{}, err
	}
	managed := a.managedPluginIDs()
	st := ConfigState{Model: config.DefaultModel(a.userDir)}
	for _, in := range cfg.Instances {
		st.Instances = append(st.Instances, ProviderInstance{
			StableID: in.StableID,
			Type:     in.Type,
			Name:     in.Name,
			API:      in.API,
			// Never echo the stored secret back to the renderer; the
			// settings page only learns whether a key exists.
			KeySet: in.KeySource == config.KeyEnv ||
				(in.KeySource == config.KeyLiteral && in.KeyValue != "") ||
				(in.KeySource == config.KeyKeychain && in.KeyValue != ""),
			KeyEnv:      in.KeySource == config.KeyEnv,
			KeyKeychain: in.KeySource == config.KeyKeychain,
			Models:      modelViews(in.Models),
			Endpoint:    in.Endpoint,
			Enabled:     in.Enabled,
			Managed:     managed[in.StableID],
		})
	}
	return st, nil
}

// ModelOptions returns the selectable per-conversation model hints in
// router priority order: every configured provider's model as
// "provider/name". The empty option (default routing policy) is implied
// by the UI and not listed here.
func (a *App) ModelOptions() ([]ModelOption, error) {
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return nil, err
	}
	out := make([]ModelOption, 0, len(cfg.Instances))
	for i, in := range cfg.Instances {
		if !in.Enabled {
			continue
		}
		for _, m := range in.Models {
			model := strings.TrimSpace(m.Name)
			if model == "" {
				continue
			}
			out = append(out, ModelOption{
				ID:        in.DeploymentID(i+1) + "/" + model,
				Label:     instanceLabel(in, i+1) + " · " + model,
				Reasoning: m.Capabilities.Reasoning.Kind != "",
			})
		}
	}
	return out, nil
}

// ModelUsage returns per-model token usage across every workspace and
// session, most used first.
func (a *App) ModelUsage() ([]ModelUsageStat, error) {
	a.mu.Lock()
	store := a.usage
	a.mu.Unlock()
	if store == nil {
		return []ModelUsageStat{}, nil
	}
	rows, err := store.Summary(a.appContext())
	if err != nil {
		return nil, err
	}
	out := make([]ModelUsageStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModelUsageStat{
			Model:           r.Model,
			InputTokens:     r.InputTokens,
			OutputTokens:    r.OutputTokens,
			CacheReadTokens: r.CacheReadTokens,
			ReasoningTokens: r.ReasoningTokens,
			LatencyMs:       r.LatencyMs,
			Workspaces:      r.Workspaces,
			Sessions:        r.Sessions,
			UpdatedAt:       r.UpdatedAt,
		})
	}
	return out, nil
}

// MemoryConfig returns the effective memory settings (embedded
// defaults overlaid with the user layer).
func (a *App) MemoryConfig() (config.MemorySettings, error) {
	return config.LoadMemory(a.userDir)
}

// SaveMemory persists the memory settings into the user configuration
// layer and rebuilds the runtime.
func (a *App) SaveMemory(settings config.MemorySettings) error {
	if settings.MaxRawMessages < 0 ||
		settings.PreserveRecent < 0 ||
		settings.MaxSummaryBytes < 0 {
		return errors.New("memory: settings must not be negative")
	}
	if err := config.WriteMemory(a.userDir, settings); err != nil {
		return err
	}
	return a.requestRebuild()
}

// ModelUsageSeries returns one model's usage bucketed by hour or day,
// oldest first. utcOffsetMinutes places day boundaries in the viewer's
// local timezone; start and end bound the recorded UTC hours
// ([start, end), empty means unbounded).
func (a *App) ModelUsageSeries(
	model string,
	granularity string,
	utcOffsetMinutes int,
	start, end string,
) ([]UsagePoint, error) {
	a.mu.Lock()
	store := a.usage
	a.mu.Unlock()
	if store == nil || model == "" {
		return []UsagePoint{}, nil
	}
	g := usage.GranularityHour
	if granularity == string(usage.GranularityDay) {
		g = usage.GranularityDay
	}
	rows, err := store.Series(
		a.appContext(),
		model,
		g,
		utcOffsetMinutes,
		start,
		end,
	)
	if err != nil {
		return nil, err
	}
	out := make([]UsagePoint, 0, len(rows))
	for _, p := range rows {
		out = append(out, UsagePoint{
			Time:            p.Time,
			InputTokens:     p.InputTokens,
			OutputTokens:    p.OutputTokens,
			CacheReadTokens: p.CacheReadTokens,
			ReasoningTokens: p.ReasoningTokens,
		})
	}
	return out, nil
}

// SaveInstances writes the inference instance configuration into the
// user configuration layer (merging over manual resources) and
// rebuilds the runtime.
func (a *App) SaveInstances(req InferenceRequest) error {
	if err := a.saveInference(req); err != nil {
		return err
	}
	return a.requestRebuild()
}

// saveInference validates and persists the inference configuration
// without rebuilding the runtime (SaveInstances adds the rebuild).
func (a *App) saveInference(req InferenceRequest) error {
	// Existing instances let an empty key ("leave blank to keep")
	// inherit the stored key instead of forcing a re-entry. A missing
	// or unparseable config is treated as a fresh install.
	existing, _ := config.LoadInference(a.userDir)
	claimed := make(map[int]bool)
	type keyedRow struct {
		idx      int    // position in instances
		name     string // request display name (error messages)
		typ      string // catalog id
		required bool   // enabled rows must end up with a key
	}
	var pending []keyedRow
	instances := make([]config.Instance, 0, len(req.Instances))
	for _, p := range req.Instances {
		prov, ok := providerByID(strings.TrimSpace(p.Type))
		if !ok {
			return fmt.Errorf("unknown provider type %q", p.Type)
		}
		in := config.Instance{
			StableID:  strings.TrimSpace(p.StableID),
			Type:      prov.ID,
			Name:      strings.TrimSpace(p.Name),
			API:       strings.TrimSpace(p.API),
			Models:    configModels(p.Models),
			Endpoint:  strings.TrimSpace(p.Endpoint),
			Enabled:   p.Enabled,
			KeySource: config.KeyLiteral,
		}
		if in.StableID == "" {
			// A row without an identity is brand new in the settings
			// page; give it one so the next save matches by id.
			in.StableID = config.NewStableID()
		}
		switch {
		case p.KeyEnv:
			in.KeySource = config.KeyEnv
			if os.Getenv(prov.EnvVar) == "" {
				return fmt.Errorf(
					"environment variable %s is not set; cannot use the env key source",
					prov.EnvVar)
			}
		case strings.TrimSpace(p.Key) != "":
			key := strings.TrimSpace(p.Key)
			// New keys go into the OS credential store when it is
			// available; the config keeps only a ${secret:...}
			// reference. A failed store write falls back to the
			// literal 0600 config so the settings page stays usable.
			if a.secrets != nil && a.secrets.Available() {
				account := secrets.AccountFor(in.DeploymentID(len(instances) + 1))
				ctx := a.appContext()
				storeErr := a.secrets.Set(ctx, account, key)
				if storeErr == nil {
					in.KeySource = config.KeyKeychain
					in.KeyValue = account
					break
				}
				telemetry.Warn(ctx,
					"opencraft: credential store write failed, falling back to config literal",
					otellog.String("error", storeErr.Error()))
			}
			in.KeyValue = key
		case p.Enabled:
			pending = append(pending, keyedRow{
				idx:      len(instances),
				name:     p.Name,
				typ:      prov.ID,
				required: true,
			})
		case strings.TrimSpace(p.StableID) != "":
			// Disabled rows with a persisted identity keep their stored
			// key too, so re-enabling needs no re-entry. Unlike enabled
			// rows, a missing stored key is not an error: the row stays
			// declared without one.
			pending = append(pending, keyedRow{
				idx:  len(instances),
				name: p.Name,
				typ:  prov.ID,
			})
		default:
			// Disabled instances may be saved without a key; they are
			// kept so re-enabling needs no re-entry.
		}
		instances = append(instances, in)
	}
	if len(pending) > 0 {
		rows := make([]config.KeyRequest, len(pending))
		for i, r := range pending {
			rows[i] = config.KeyRequest{
				StableID: strings.TrimSpace(req.Instances[r.idx].StableID),
				Type:     r.typ,
				Name:     strings.TrimSpace(req.Instances[r.idx].Name),
				Models:   requestModelNames(req.Instances[r.idx].Models),
				Endpoint: strings.TrimSpace(req.Instances[r.idx].Endpoint),
				API:      strings.TrimSpace(req.Instances[r.idx].API),
			}
		}
		idxs, ok := config.MatchStoredKeys(existing.Instances, rows, claimed)
		if !ok {
			for i, idx := range idxs {
				if idx >= 0 || !pending[i].required {
					continue
				}
				return fmt.Errorf(
					"instance %s (%s): an API key or the env key source is required",
					pending[i].name, pending[i].typ)
			}
		}
		for i, idx := range idxs {
			if idx < 0 {
				// Optional row (disabled, no stored key) stays keyless.
				continue
			}
			dst := &instances[pending[i].idx]
			dst.KeySource = existing.Instances[idx].KeySource
			dst.KeyValue = existing.Instances[idx].KeyValue
		}
	}
	// Plugin-managed deployments are owned by their capability plugin:
	// content edits and removals from the settings page are rolled back
	// to the stored config (order/priority stays user-controlled), and
	// the frontend is reminded so the silent restore is visible.
	instances, restored := restoreManagedInstances(
		existing.Instances, instances, a.managedPluginIDs())
	cfg := config.InferenceConfig{Instances: instances}
	if len(cfg.Enabled()) == 0 {
		return errors.New("enable at least one instance")
	}
	if err := config.WriteInference(a.userDir, cfg); err != nil {
		return err
	}
	if len(restored) > 0 && a.bridge != nil {
		a.bridge.Emit("managed_restored", map[string]any{"ids": restored})
	}
	return nil
}

// managedPluginIDs returns the set of installed plugin ids. Deployments
// whose stable id matches one of them are plugin-managed and may not be
// edited or removed through the settings page.
func (a *App) managedPluginIDs() map[string]bool {
	if a.plugins == nil {
		return nil
	}
	plugins, err := a.plugins.List()
	if err != nil {
		return nil
	}
	ids := make(map[string]bool, len(plugins))
	for _, p := range plugins {
		ids[p.ID] = true
	}
	return ids
}

// restoreManagedInstances reconciles the settings-page request against
// plugin-managed deployments in the stored config. Managed rows keep
// their request position (the user may reorder priority freely) but
// their content is taken from the stored config whenever the request
// edited or dropped them; the restored ids are returned for the
// reminder toast.
func restoreManagedInstances(
	existing, requested []config.Instance,
	managed map[string]bool,
) ([]config.Instance, []string) {
	if len(managed) == 0 {
		return requested, nil
	}
	byID := make(map[string]config.Instance, len(existing))
	for _, in := range existing {
		if managed[in.StableID] {
			byID[in.StableID] = in
		}
	}
	if len(byID) == 0 {
		return requested, nil
	}
	out := make([]config.Instance, 0, len(requested)+len(byID))
	var restored []string
	seen := make(map[string]bool, len(byID))
	for _, in := range requested {
		orig, ok := byID[in.StableID]
		if !ok {
			out = append(out, in)
			continue
		}
		seen[in.StableID] = true
		if !sameInstanceContent(orig, in) {
			restored = append(restored, in.StableID)
			out = append(out, orig)
			continue
		}
		out = append(out, in)
	}
	for _, in := range existing {
		if managed[in.StableID] && !seen[in.StableID] {
			restored = append(restored, in.StableID)
			out = append(out, in)
		}
	}
	return out, restored
}

// sameInstanceContent compares the non-secret fields the settings page
// may edit; key handling stays with the stored-key matching logic.
func sameInstanceContent(a, b config.Instance) bool {
	if a.StableID != b.StableID || a.Type != b.Type || a.Name != b.Name ||
		a.API != b.API || a.Endpoint != b.Endpoint || a.Enabled != b.Enabled ||
		len(a.Models) != len(b.Models) {
		return false
	}
	for i := range a.Models {
		if !sameModel(a.Models[i], b.Models[i]) {
			return false
		}
	}
	return true
}

// sameModel compares two model declarations including their declared
// capabilities and per-model endpoint.
func sameModel(a, b config.Model) bool {
	return a.Name == b.Name && a.Kind == b.Kind && a.Endpoint == b.Endpoint &&
		a.Responses == b.Responses && a.Dimensions == b.Dimensions &&
		a.EffortNone == b.EffortNone &&
		slices.Equal(a.Capabilities.Inputs, b.Capabilities.Inputs) &&
		slices.Equal(a.Capabilities.Outputs, b.Capabilities.Outputs) &&
		a.Capabilities.Reasoning.Kind == b.Capabilities.Reasoning.Kind &&
		maps.Equal(
			a.Capabilities.Reasoning.EffortMap,
			b.Capabilities.Reasoning.EffortMap,
		) &&
		a.Capabilities.HostedWebSearch == b.Capabilities.HostedWebSearch
}

// instanceIDFor derives the positional display id for instance n
// (1-based). It is cosmetic only — routing ids come from
// Instance.DeploymentID and are stable.
func instanceIDFor(instanceType string, n int) string {
	return fmt.Sprintf("%s-%d", instanceType, n)
}

// instanceLabel returns the display label for one instance.
func instanceLabel(in config.Instance, n int) string {
	if in.Name != "" {
		return in.Name
	}
	return instanceIDFor(in.Type, n)
}

// modelViews renders config models for the settings page.
func modelViews(models []config.Model) []ModelView {
	out := make([]ModelView, 0, len(models))
	for _, m := range models {
		out = append(out, ModelView{
			Name:               m.Name,
			Inputs:             config.PartKindStrings(m.Capabilities.Inputs),
			Outputs:            config.PartKindStrings(m.Capabilities.Outputs),
			Kind:               m.Kind,
			Reasoning:          string(m.Capabilities.Reasoning.Kind),
			ReasoningEffortMap: config.EffortMapStrings(m.Capabilities.Reasoning.EffortMap),
			EffortNone:         m.EffortNone,
			Dimensions:         m.Dimensions,
			WebSearch:          m.Capabilities.HostedWebSearch,
			Endpoint:           m.Endpoint,
		})
	}
	return out
}

// configModels converts the settings-page model rows into config models.
func configModels(views []ModelView) []config.Model {
	out := make([]config.Model, 0, len(views))
	for _, v := range views {
		out = append(out, config.Model{
			Name: strings.TrimSpace(v.Name),
			Kind: strings.TrimSpace(v.Kind),
			Capabilities: inference.ModelCapabilities{
				Inputs:  config.ToPartKinds(v.Inputs),
				Outputs: config.ToPartKinds(v.Outputs),
				Reasoning: inference.ReasoningCapability{
					Kind:      inference.ReasoningKind(strings.TrimSpace(v.Reasoning)),
					EffortMap: config.EffortMapEfforts(v.ReasoningEffortMap),
				},
				HostedWebSearch: v.WebSearch,
			},
			Endpoint:   strings.TrimSpace(v.Endpoint),
			EffortNone: v.EffortNone,
			Dimensions: v.Dimensions,
		})
	}
	return out
}

// requestModelNames extracts the non-empty model names of a request row
// for stored-key fingerprinting.
func requestModelNames(views []ModelView) []string {
	var names []string
	for _, v := range views {
		if name := strings.TrimSpace(v.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// MCPConfig returns the configured MCP tool servers from the user
// configuration layer.
func (a *App) MCPConfig() ([]config.MCPServer, error) {
	return config.LoadMCP(a.userDir)
}

// SaveMCP persists the MCP tool server list into the user
// configuration layer (merging over manual resources) and rebuilds the
// runtime.
func (a *App) SaveMCP(servers []config.MCPServer) error {
	for i := range servers {
		if err := validateMCPServer(&servers[i]); err != nil {
			return err
		}
	}
	if err := config.WriteMCP(a.userDir, servers); err != nil {
		return err
	}
	return a.requestRebuild()
}

// validateMCPServer normalizes and validates one MCP server entry the
// same way the save path does, so TestMCP and SaveMCP agree on what is
// a well-formed server.
func validateMCPServer(srv *config.MCPServer) error {
	srv.Name = strings.TrimSpace(srv.Name)
	srv.Transport = strings.TrimSpace(srv.Transport)
	srv.Command = strings.TrimSpace(srv.Command)
	srv.URL = strings.TrimSpace(srv.URL)
	switch srv.Transport {
	case "stdio":
		if srv.Command == "" {
			return fmt.Errorf("MCP server %q: command is required for stdio", srv.Name)
		}
	case "http":
		if srv.URL == "" {
			return fmt.Errorf("MCP server %q: url is required for http", srv.Name)
		}
	default:
		return fmt.Errorf("MCP server %q: transport must be stdio or http", srv.Name)
	}
	if srv.Name == "" {
		return errors.New("MCP server: name is required")
	}
	return nil
}

// mcpTransport builds the MCP transport for one server entry the same
// way the runtime factory wires stdio/http servers.
func mcpTransport(server config.MCPServer) (mcpsdk.Transport, error) {
	switch server.Transport {
	case "stdio":
		return mcp.Stdio(server.Command, server.Args, server.Env)
	case "http":
		return mcp.StreamableHTTP(server.URL, nil, nil)
	default:
		return nil, fmt.Errorf("MCP server %q: transport must be stdio or http", server.Name)
	}
}

// TestMCP verifies that one MCP server can connect and expose its
// tools using a throwaway source, so neither the live runtime nor the
// saved configuration is touched. It waits up to 15s for the first
// connection; the returned error is the test result.
func (a *App) TestMCP(server config.MCPServer) error {
	if err := validateMCPServer(&server); err != nil {
		return err
	}
	const timeout = 15 * time.Second
	ctx, cancel := context.WithTimeout(a.appContext(), timeout)
	defer cancel()
	src := mcp.NewSource(mcp.WithConnectTimeout(timeout))
	defer func() { _ = src.Close() }()
	transport, err := mcpTransport(server)
	if err != nil {
		return err
	}
	if err := src.AddServer(ctx, server.Name, transport); err != nil {
		return err
	}
	return src.WaitReady(ctx, server.Name, timeout)
}

// MCPStatus probes the live runtime's MCP source and reports each
// configured server's connection state: connected (tools published),
// connecting (first attempt or background retry pending), or error
// (the background loop gave up on it).
func (a *App) MCPStatus() ([]MCPStatusDTO, error) {
	servers, err := config.LoadMCP(a.userDir)
	if err != nil {
		return nil, err
	}
	workDir := a.snapshotWorkDir()
	ctrl := a.controller()
	var src *mcp.Source
	if ctrl != nil && ctrl.Runtime() != nil {
		if value, ok := ctrl.Runtime().Resource("tool.mcp"); ok {
			if s, ok := value.(*mcp.Source); ok {
				src = s
			}
		}
	}
	out := make([]MCPStatusDTO, 0, len(servers))
	for _, srv := range servers {
		dto := MCPStatusDTO{Name: srv.Name}
		if src == nil {
			if strings.TrimSpace(workDir) == "" {
				dto.Status = "error"
				dto.Error = "MCP runtime is not ready; open a workspace first"
			} else {
				dto.Status = "connecting" // runtime is being assembled
			}
		} else {
			ctx, cancel := context.WithTimeout(
				a.appContext(), 250*time.Millisecond)
			probeErr := src.WaitReady(ctx, srv.Name, 200*time.Millisecond)
			cancel()
			switch {
			case probeErr == nil:
				dto.Status = "connected"
			case errdefs.IsTimeout(probeErr):
				dto.Status = "connecting"
			default:
				dto.Status = "error"
				dto.Error = probeErr.Error()
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

// Reload rebuilds the runtime from the current configuration.
func (a *App) Reload() error {
	return a.requestRebuild()
}

// Workspace returns the workspace directory the app operates on.
func (a *App) Workspace() string {
	return a.snapshotWorkDir()
}

// StartTurn starts one assistant turn with the given user message
// (role + text/image/audio/video/file parts) and returns immediately.
// Stream deltas arrive over the UI event channel. Local attachments
// are persisted into the session's media/ and files/ directories
// first; the archive keeps their URL paths while the opencraft.media
// prepare hook inlines the bytes before the model call.
func (a *App) StartTurn(req StartTurnRequest) (TurnStart, error) {
	contextID := strings.TrimSpace(req.ContextID)
	wd := a.snapshotWorkDir()
	if !ocsessions.ValidID(contextID) {
		return TurnStart{}, fmt.Errorf("invalid session id %q", contextID)
	}
	store := a.sessionStore()
	mode := ocsessions.ModeWorkspace
	think := string(ocsessions.ThinkMedium)
	model := ""
	if store != nil {
		ctx := a.appContext()
		if m, err := store.Mode(ctx, contextID); err == nil {
			mode = m
		}
		if lvl, err := store.Think(ctx, contextID); err == nil {
			think = string(lvl)
		}
		if mdl, err := store.Model(ctx, contextID); err == nil {
			model = mdl
		}
	}
	return a.startTurn(req.Message, contextID, mode, think, model, wd, nil)
}

// startTurn starts one assistant turn in an explicit conversation
// context (used by the UI conversation and by automation runs, which
// must never touch the UI's active session state). It returns
// immediately; when done is non-nil, waitTurn delivers the terminal
// TurnEnd on it after the turn_end UI event.
func (a *App) startTurn(
	msg message.Message,
	contextID string,
	mode ocsessions.Mode,
	think, model, wd string,
	done chan<- TurnEnd,
) (TurnStart, error) {
	if strings.TrimSpace(wd) == "" {
		return TurnStart{}, errors.New("no workspace selected: pick a folder first")
	}
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h == nil || !a.inCurrentWorkspace(wd) {
		return TurnStart{}, errors.New(
			"runtime is not ready: configure inference in Settings first")
	}
	store := h.Sessions()
	if store == nil {
		return TurnStart{}, errors.New("runtime is not ready: session store missing")
	}
	if err := validateUserMessage(msg); err != nil {
		return TurnStart{}, err
	}
	text := msg.Content.Text()
	if strings.TrimSpace(text) == "" && !hasMediaParts(msg.Content.Parts) {
		return TurnStart{}, errors.New("message is required")
	}
	if hasMediaParts(msg.Content.Parts) {
		parts, err := persistAttachments(store, contextID, msg.Content.Parts)
		if err != nil {
			return TurnStart{}, fmt.Errorf("persist attachment: %w", err)
		}
		msg.Content.Parts = parts
	}
	// Only send a reasoning knob when the effective model declares a
	// reasoning capability: drivers reject reasoning_effort for models
	// without one. The think level stays the per-session default
	// (medium) for capable models and is dropped for the rest.
	if cfg, err := config.LoadInference(a.userDir); err == nil &&
		!cfg.ModelReasoning(model) {
		think = ""
	}
	ctx := a.appContext()
	a.fireHooks(ctx, hooks.EventUserPromptSubmit, map[string]any{
		"event":           hooks.EventUserPromptSubmit,
		"conversation_id": contextID,
		"prompt":          text,
	})
	requestedAt := time.Now().UTC()
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:   msg,
		ContextID: contextID,
		Mode:      mode,
		Think:     think,
		Model:     model,
		Sink:      agent.StreamSinkFunc(a.bridge.Sink),
		QueueSize: 256,
		OnUsage:   a.onUsage,
		Undo:      a.undoStoreFor(wd),
	})
	if err != nil {
		return TurnStart{}, fmt.Errorf("start turn: %w", err)
	}
	startedAt := time.Now().UTC()
	runID := run.RunID()

	a.mu.Lock()
	if a.convRuns == nil {
		a.convRuns = make(map[string]map[string]bool)
	}
	if a.convRuns[contextID] == nil {
		a.convRuns[contextID] = make(map[string]bool)
	}
	a.convRuns[contextID][runID] = true
	a.mu.Unlock()
	go a.waitTurn(h, run, contextID, done)
	return TurnStart{
		RunID:       runID,
		ContextID:   contextID,
		RequestedAt: requestedAt.Format(time.RFC3339),
		StartedAt:   startedAt.Format(time.RFC3339),
	}, nil
}

// NewChat starts a fresh conversation: a new session context is
// minted, so subsequent turns keep their own history and permission
// mode. The conversation resets to workspace mode.
func (a *App) NewChat() (SessionSnapshot, error) {
	a.mu.Lock()
	a.conversationID = ocsessions.NewID()
	a.mode = ocsessions.ModeWorkspace
	a.think = string(ocsessions.ThinkMedium)
	a.model = ""
	id := a.conversationID
	snapshot := SessionSnapshot{
		SessionID: id,
		Mode:      string(a.mode),
		Think:     a.think,
		Model:     a.model,
	}
	// Register the id in the in-memory conversation index so
	// ResumeSession accepts it before its first turn persists history
	// (store.List only surfaces sessions with history or usage).
	if a.convRuns == nil {
		a.convRuns = make(map[string]map[string]bool)
	}
	a.convRuns[id] = make(map[string]bool)
	a.mu.Unlock()
	return snapshot, nil
}

// SessionMode returns the sandbox permission mode of the current
// conversation ("workspace", "read-only", or "yolo").
func (a *App) SessionMode() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.mode), nil
}

// SetSessionMode switches the conversation's sandbox permission mode.
// The change is persisted for the conversation and therefore also
// applies to commands still running in the current turn.
func (a *App) SetSessionMode(mode string) error {
	m := ocsessions.Mode(mode)
	switch m {
	case ocsessions.ModeWorkspace, ocsessions.ModeReadOnly, ocsessions.ModeYOLO:
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	a.mu.Lock()
	a.mode = m
	contextID := a.conversationID
	a.mu.Unlock()
	store := a.sessionStore()
	if store == nil {
		return nil
	}
	if err := store.SetMode(a.appContext(), contextID, m); err != nil {
		return fmt.Errorf("persist permission mode: %w", err)
	}
	return nil
}

// ReplyPrompt answers one pending interaction rendered by the UI.
func (a *App) ReplyPrompt(promptID string, req ReplyRequest) (bool, error) {
	return a.bridge.Answer(promptID, req)
}

// CancelTurn cancels a running turn; the turn's terminal event still
// arrives so the UI can finalize the run.
func (a *App) CancelTurn(runID string) error {
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h == nil {
		return errors.New("runtime is not ready")
	}
	return h.CancelRun(runID)
}

// ListAgents returns the persisted subagents (registry view; the
// runtime owns the live delegation targets).
func (a *App) ListAgents() []AgentSummary {
	lifecycle := a.agentLifecycle()
	if lifecycle == nil {
		// Runtime not ready yet (startup) or agents unavailable: return
		// an empty list, never null (the UI iterates the result).
		return []AgentSummary{}
	}
	return lifecycle.List()
}

// AgentDetail returns one persisted subagent's declaration with its
// graph definition parsed for the visual editor.
func (a *App) AgentDetail(name string) (AgentDetail, error) {
	lifecycle := a.agentLifecycle()
	if lifecycle == nil {
		return AgentDetail{}, errors.New("agent registry is not available")
	}
	spec, err := lifecycle.Detail(a.appContext(), name)
	if err != nil {
		return AgentDetail{}, err
	}
	var g Graph
	if err := yaml.Unmarshal([]byte(spec.Graph), &g); err != nil {
		return AgentDetail{}, fmt.Errorf("parse subagent graph: %w", err)
	}
	return AgentDetail{
		Name:        spec.Name,
		Description: spec.Description,
		Graph:       g,
		CreatedAt:   spec.CreatedAt.Format(time.RFC3339),
	}, nil
}

// UpdateAgent updates one persisted subagent's description and/or
// graph definition. The runtime registration is swapped after
// in-flight delegations drain; the name is immutable.
func (a *App) UpdateAgent(
	name, description, graph string,
) (AgentUpdateResult, error) {
	lifecycle := a.agentLifecycle()
	if lifecycle == nil {
		return AgentUpdateResult{}, errors.New("agent registry is not available")
	}
	res, err := lifecycle.Update(a.appContext(), name, description, graph)
	if err != nil {
		return AgentUpdateResult{}, err
	}
	return AgentUpdateResult{
		Name:        res.Name,
		Description: res.Description,
		PersistedTo: res.PersistedTo,
		CreatedAt:   res.CreatedAt.Format(time.RFC3339),
	}, nil
}

// UnregisterAgent removes a persisted subagent (draining in-flight
// delegations) and deletes its declaration directory.
func (a *App) UnregisterAgent(name string) error {
	lifecycle := a.agentLifecycle()
	if lifecycle == nil {
		return errors.New("agent registry is not available")
	}
	return lifecycle.Remove(a.appContext(), name)
}

// openCommand returns the platform opener for files/directories.
func openCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path)
	case "windows":
		// rundll32's FileProtocolHandler invokes ShellExecute semantics
		// without going through cmd.exe, so a path containing shell
		// metacharacters cannot be reinterpreted as a command line.
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		return exec.Command("xdg-open", path)
	}
}

func (a *App) waitTurn(
	h *host.Host,
	run *host.Run,
	contextID string,
	done chan<- TurnEnd,
) {
	ctx := a.appContext()
	res, err := run.Wait(ctx)
	runID := run.RunID()

	a.mu.Lock()
	turnUsage := a.runUsage[runID]
	delete(a.runUsage, runID)
	usageStore := a.usage
	a.mu.Unlock()

	finishedAt := time.Now().UTC()
	end := TurnEnd{
		RunID:          runID,
		ConversationID: contextID,
		Status:         "unknown",
		FinishedAt:     finishedAt.Format(time.RFC3339),
	}
	if res != nil {
		end.Status = string(res.Status)
		if res.Err != nil {
			end.Error = res.Err.Error()
		}
	}
	if err != nil && end.Error == "" {
		end.Error = err.Error()
	}
	persistCtx := context.WithoutCancel(ctx)
	if usageStore != nil && turnUsage.Model != "" {
		_ = usageStore.Record(
			persistCtx,
			workspaceID(h.WorkDir()),
			contextID,
			turnUsage.Model,
			usage.Usage{
				InputTokens:     turnUsage.InputTokens,
				OutputTokens:    turnUsage.OutputTokens,
				CacheReadTokens: turnUsage.CacheReadTokens,
				ReasoningTokens: turnUsage.ReasoningTokens,
				LatencyMs:       turnUsage.LatencyMs,
			},
		)
	}
	a.bridge.Emit("turn_end", end)
	if done != nil {
		select {
		case done <- end:
		default:
		}
	}
	a.emitUndoState(contextID)
	a.fireHooks(a.appContext(), hooks.EventTurnEnd, map[string]any{
		"event":           hooks.EventTurnEnd,
		"conversation_id": contextID,
		"run_id":          runID,
		"status":          end.Status,
		"error":           end.Error,
		"usage": map[string]int64{
			"input_tokens":     turnUsage.InputTokens,
			"output_tokens":    turnUsage.OutputTokens,
			"total_tokens":     turnUsage.TotalTokens,
			"reasoning_tokens": turnUsage.ReasoningTokens,
		},
	})
	// A plugin toggle requested during the turn is applied now that
	// this turn is no longer running (and no other turn is active).
	a.maybeApplyPendingRebuild()
}

// UndoChange reverts the latest captured turn's file changes for the
// current conversation and returns the restored paths.
func (a *App) UndoChange() ([]string, error) {
	wd := a.snapshotWorkDir()
	st := a.undoStoreFor(wd)
	id := a.currentConversationID()
	if st == nil {
		return nil, errors.New("undo is unavailable")
	}
	files, err := st.Undo(a.appContext(), id)
	if err != nil {
		return nil, err
	}
	a.emitUndoState(id)
	return files, nil
}

// RedoChange re-applies the latest undone turn's changes for the
// current conversation and returns the restored paths.
func (a *App) RedoChange() ([]string, error) {
	wd := a.snapshotWorkDir()
	st := a.undoStoreFor(wd)
	id := a.currentConversationID()
	if st == nil {
		return nil, errors.New("undo is unavailable")
	}
	files, err := st.Redo(a.appContext(), id)
	if err != nil {
		return nil, err
	}
	a.emitUndoState(id)
	return files, nil
}

// UndoState reports whether undo/redo have anything to apply for the
// current conversation.
func (a *App) UndoState() (undo.State, error) {
	wd := a.snapshotWorkDir()
	st := a.undoStoreFor(wd)
	id := a.currentConversationID()
	if st == nil {
		return undo.State{}, nil
	}
	return st.Available(a.appContext(), id)
}

// emitUndoState pushes the current undo availability to the UI.
func (a *App) emitUndoState(contextID string) {
	if a.bridge == nil {
		return
	}
	st := a.undoStoreFor(a.snapshotWorkDir())
	if st == nil {
		return
	}
	state, err := st.Available(a.appContext(), contextID)
	if err != nil {
		return
	}
	a.bridge.Emit("undo_state", state)
}
