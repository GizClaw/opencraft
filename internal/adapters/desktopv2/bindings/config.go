package bindings

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	"github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Config is the settings/config binding object.
type Config struct {
	core *core.Core
}

// NewConfig wires the config binding to the core workspace service.
func NewConfig(c *core.Core) *Config {
	return &Config{core: c}
}

// Version returns the application version.
func (b *Config) Version() string {
	return telemetry.ServiceVersion
}

// ConfigStatus is the binding-side alias of the core status DTO.
type ConfigStatus = core.ConfigStatus

// ConfigStatus reports the current configuration state.
func (b *Config) ConfigStatus() (ConfigStatus, error) {
	return b.core.ConfigStatus(), nil
}

// ProviderView is one entry of the provider catalog.
type ProviderView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultModel  string `json:"default_model"`
	EnvVar        string `json:"env_var"`
	API           string `json:"api"`
	Azure         bool   `json:"azure"`
	ModelEndpoint bool   `json:"model_endpoint"`
}

// Providers returns the provider catalog.
func (b *Config) Providers() []ProviderView {
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

// ModelCatalog returns every driver's built-in model catalog.
func (b *Config) ModelCatalog() ([]config.ProviderModels, error) {
	return config.ModelCatalog()
}

// ModelView is one model exposed by an inference instance.
type ModelView struct {
	Name               string            `json:"name"`
	Kind               string            `json:"kind,omitempty"`
	Inputs             []string          `json:"inputs"`
	Outputs            []string          `json:"outputs"`
	Reasoning          string            `json:"reasoning"`
	ReasoningEffortMap map[string]string `json:"reasoning_effort_map,omitempty"`
	EffortNone         bool              `json:"effort_none,omitempty"`
	Dimensions         bool              `json:"dimensions,omitempty"`
	WebSearch          bool              `json:"web_search"`
	Endpoint           string            `json:"endpoint"`
}

// ProviderInstance is one inference instance in router priority order.
type ProviderInstance struct {
	StableID    string      `json:"stable_id"`
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	API         string      `json:"api"`
	Key         string      `json:"key"`
	KeySet      bool        `json:"key_set"`
	KeyEnv      bool        `json:"key_env"`
	KeyKeychain bool        `json:"key_keychain"`
	Models      []ModelView `json:"models"`
	Endpoint    string      `json:"endpoint"`
	Enabled     bool        `json:"enabled"`
	Managed     bool        `json:"managed"`
}

// ConfigState is the full inference wiring the settings page edits.
type ConfigState struct {
	Model     string             `json:"model"`
	Instances []ProviderInstance `json:"instances"`
}

// ConfigState returns the configured inference wiring plus the current
// default model.
func (b *Config) ConfigState() (ConfigState, error) {
	cfg, err := config.LoadInference(b.core.UserDir)
	if err != nil {
		return ConfigState{}, err
	}
	managed := b.managedPluginIDs()
	st := ConfigState{Model: config.DefaultModel(b.core.UserDir)}
	for _, in := range cfg.Instances {
		st.Instances = append(st.Instances, ProviderInstance{
			StableID: in.StableID,
			Type:     in.Type,
			Name:     in.Name,
			API:      in.API,
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

// ModelOption is one selectable per-conversation model hint.
type ModelOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Reasoning bool   `json:"reasoning"`
}

// ModelOptions returns selectable model hints in router priority order.
func (b *Config) ModelOptions() ([]ModelOption, error) {
	cfg, err := config.LoadInference(b.core.UserDir)
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

// ModelUsageStat is one model's cumulative user-level usage.
type ModelUsageStat struct {
	Model           string `json:"model"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	LatencyMs       int64  `json:"latency_ms"`
	Workspaces      int    `json:"workspaces"`
	Sessions        int    `json:"sessions"`
	UpdatedAt       string `json:"updated_at"`
}

// ModelUsage returns per-model token usage.
func (b *Config) ModelUsage() ([]ModelUsageStat, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Usage()
	if store == nil {
		return []ModelUsageStat{}, nil
	}
	rows, err := store.Summary(ctx)
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

// UsagePoint is one time-bucketed usage sample.
type UsagePoint struct {
	Time            string `json:"time"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}

// ModelUsageSeries returns one model's bucketed usage.
func (b *Config) ModelUsageSeries(
	model string,
	granularity string,
	utcOffsetMinutes int,
	start, end string,
) ([]UsagePoint, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Usage()
	if store == nil || model == "" {
		return []UsagePoint{}, nil
	}
	g := usage.GranularityHour
	if granularity == string(usage.GranularityDay) {
		g = usage.GranularityDay
	}
	rows, err := store.Series(
		ctx, model, g, utcOffsetMinutes, start, end,
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

// MemoryConfig returns the effective memory settings.
func (b *Config) MemoryConfig() (config.MemorySettings, error) {
	return config.LoadMemory(b.core.UserDir)
}

// SaveMemory persists memory settings.
func (b *Config) SaveMemory(settings config.MemorySettings) error {
	if settings.MaxRawMessages < 0 ||
		settings.PreserveRecent < 0 ||
		settings.MaxSummaryBytes < 0 {
		return errors.New("memory: settings must not be negative")
	}
	if err := config.WriteMemory(b.core.UserDir, settings); err != nil {
		return err
	}
	return b.core.ReloadRuntime(b.core.Shell.Context())
}

// SaveInstances persists inference instances and invalidates pooled
// hosts so the next Acquire rebuilds from the new configuration.
func (b *Config) SaveInstances(req InferenceRequest) error {
	if err := b.saveInference(req); err != nil {
		return err
	}
	return b.core.ReloadRuntime(b.core.Shell.Context())
}

// MCPConfig returns configured MCP tool servers.
func (b *Config) MCPConfig() ([]config.MCPServer, error) {
	return config.LoadMCP(b.core.UserDir)
}

// SaveMCP persists and reloads MCP tool servers.
func (b *Config) SaveMCP(servers []config.MCPServer) error {
	ctx := b.core.Shell.Context()
	for i := range servers {
		if err := validateMCPServer(&servers[i]); err != nil {
			return err
		}
	}
	if err := config.WriteMCP(b.core.UserDir, servers); err != nil {
		return err
	}
	return b.core.ReloadRuntime(ctx)
}

// TestMCP verifies one MCP server can connect.
func (b *Config) TestMCP(
	server config.MCPServer,
) error {
	ctx := b.core.Shell.Context()
	if err := validateMCPServer(&server); err != nil {
		return err
	}
	const timeout = 15 * time.Second
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	src := mcp.NewSource(mcp.WithConnectTimeout(timeout))
	defer func() { _ = src.Close() }()
	transport, err := mcpTransport(server)
	if err != nil {
		return err
	}
	if err := src.AddServer(testCtx, server.Name, transport); err != nil {
		return err
	}
	return src.WaitReady(testCtx, server.Name, timeout)
}

// MCPStatusDTO is one MCP server's connection state.
type MCPStatusDTO struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// MCPStatus probes live MCP sources.
func (b *Config) MCPStatus() ([]MCPStatusDTO, error) {
	ctx := b.core.Shell.Context()
	servers, err := config.LoadMCP(b.core.UserDir)
	if err != nil {
		return nil, err
	}
	var src *mcp.Source
	if h := b.core.Runtime.Current(); h != nil &&
		h.Controller() != nil && h.Controller().Runtime() != nil {
		if v, ok := h.Controller().Runtime().Resource("tool.mcp"); ok {
			src, _ = v.(*mcp.Source)
		}
	}
	out := make([]MCPStatusDTO, 0, len(servers))
	for _, srv := range servers {
		dto := MCPStatusDTO{Name: srv.Name}
		if src == nil {
			dto.Status = "connecting"
		} else {
			probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			err := src.WaitReady(probeCtx, srv.Name, 200*time.Millisecond)
			cancel()
			if err == nil {
				dto.Status = "connected"
			} else {
				dto.Status = "error"
				dto.Error = err.Error()
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

// Reload rebuilds the runtime from current configuration.
func (b *Config) Reload() error {
	ctx := b.core.Shell.Context()
	return b.core.ReloadRuntime(ctx)
}

func validateMCPServer(srv *config.MCPServer) error {
	srv.Name = strings.TrimSpace(srv.Name)
	srv.Transport = strings.TrimSpace(srv.Transport)
	srv.Command = strings.TrimSpace(srv.Command)
	srv.URL = strings.TrimSpace(srv.URL)
	if srv.Name == "" {
		return errors.New("MCP server: name is required")
	}
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
	return nil
}

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

// InferenceRequest is the settings-page inference payload.
type InferenceRequest struct {
	Instances []ProviderInstance `json:"instances"`
}

func (b *Config) saveInference(req InferenceRequest) error {
	// Existing instances let an empty key ("leave blank to keep")
	// inherit the stored key instead of forcing a re-entry. A missing
	// or unparseable config is treated as a fresh install.
	existing, _ := config.LoadInference(b.core.UserDir)
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
			if b.core.Plugin.Secrets != nil && b.core.Plugin.Secrets.Available() {
				account := secrets.AccountFor(in.DeploymentID(len(instances) + 1))
				storeErr := b.core.Plugin.Secrets.Set(
					b.core.Shell.Context(), account, key,
				)
				if storeErr == nil {
					in.KeySource = config.KeyKeychain
					in.KeyValue = account
					break
				}
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
		existing.Instances, instances, b.managedPluginIDs(),
	)
	cfg := config.InferenceConfig{Instances: instances}
	if len(cfg.Enabled()) == 0 {
		return errors.New("enable at least one instance")
	}
	if err := config.WriteInference(b.core.UserDir, cfg); err != nil {
		return err
	}
	if len(restored) > 0 {
		b.core.Shell.Emit(
			"managed_restored", map[string]any{"ids": restored},
		)
	}
	return nil
}

// managedPluginIDs returns the set of installed plugin ids. Deployments
// whose stable id matches one of them are plugin-managed and may not be
// edited or removed through the settings page.
func (b *Config) managedPluginIDs() map[string]bool {
	if b.core.Plugin == nil || b.core.Plugin.Store == nil {
		return nil
	}
	installed, err := b.core.Plugin.Store.List()
	if err != nil {
		return nil
	}
	ids := make(map[string]bool, len(installed))
	for _, p := range installed {
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

func providerByID(id string) (config.Provider, bool) {
	for _, p := range config.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return config.Provider{}, false
}

func instanceLabel(in config.Instance, n int) string {
	if in.Name != "" {
		return in.Name
	}
	return fmt.Sprintf("%s-%d", in.Type, n)
}

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

// requestModelNames extracts the non-empty model names of a request
// row for stored-key fingerprinting.
func requestModelNames(views []ModelView) []string {
	var names []string
	for _, v := range views {
		if name := strings.TrimSpace(v.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
