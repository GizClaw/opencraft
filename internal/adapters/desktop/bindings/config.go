package bindings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	flowtelemetry "github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/foundation/version"
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
	return version.ServiceVersion
}

// BuildProfile reports immutable flags selected by Go build tags (see
// foundation/profile). The desktop shell reads this once at startup;
// the Go binary remains the single source of truth for the profile.
type BuildProfile struct {
	YoloOnly bool `json:"yolo_only"`
}

// Profile returns the active build profile.
func (b *Config) Profile() BuildProfile {
	return BuildProfile{YoloOnly: profile.YoloOnly()}
}

// ConfigStatus is the binding-side alias of the core status DTO.
type ConfigStatus = core.ConfigStatus

// ConfigStatus reports the current configuration state.
func (b *Config) ConfigStatus() (ConfigStatus, error) {
	return b.core.ConfigStatus(), nil
}

// ProviderView is one inference driver the settings page can build an
// instance from. It carries no vendor defaults: the endpoint, the API
// surface, the wire dialect and the models are deployment data.
type ProviderView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	EnvVar        string `json:"env_var"`
	ModelEndpoint bool   `json:"model_endpoint"`
	// Impl is the driver this entry registers. It equals ID today; it
	// stays separate so a plugin-declared driver can reuse an entry.
	Impl string `json:"impl"`
}

// Providers returns the drivers an instance can be built from.
func (b *Config) Providers() []ProviderView {
	out := make([]ProviderView, 0, len(config.Providers))
	for _, p := range config.Providers {
		out = append(out, ProviderView{
			ID:            p.ID,
			Name:          p.Name,
			EnvVar:        p.EnvVar,
			ModelEndpoint: p.ModelEndpoint,
			Impl:          p.Impl,
		})
	}
	return out
}

// InferenceModelView is one built-in model the settings page can
// prefill a model row from. Model carries the declaration in the same
// shape a save submits, so the page never needs a second vocabulary.
type InferenceModelView struct {
	ID     string           `json:"id"`
	Type   string           `json:"type"`
	Vendor string           `json:"vendor,omitempty"`
	Label  string           `json:"label,omitempty"`
	Source string           `json:"source,omitempty"`
	Model  config.ModelSpec `json:"model"`
}

// InferenceTemplateView is one built-in starter instance: the
// provider-level fields plus the resolved models the new row starts
// with, in router priority order.
type InferenceTemplateView struct {
	ID       string                  `json:"id"`
	Label    string                  `json:"label"`
	Type     string                  `json:"type"`
	Vendor   string                  `json:"vendor,omitempty"`
	API      string                  `json:"api,omitempty"`
	Endpoint string                  `json:"endpoint,omitempty"`
	Advanced config.InstanceAdvanced `json:"advanced"`
	Notes    string                  `json:"notes,omitempty"`
	Models   []config.ModelSpec      `json:"models"`
}

// InferenceCatalogState is the built-in inference template catalog.
type InferenceCatalogState struct {
	Version   string                  `json:"version"`
	Templates []InferenceTemplateView `json:"templates"`
	Models    []InferenceModelView    `json:"models"`
}

// InferenceCatalog returns the catalog the settings page prefills new
// instances and model rows from. Nothing in it is applied on its own:
// the page submits the same InstanceSpec it would submit for a
// hand-typed row, so the catalog stays a prefill layer.
func (b *Config) InferenceCatalog() (InferenceCatalogState, error) {
	catalog, err := config.LoadInferenceCatalog()
	if err != nil {
		return InferenceCatalogState{}, err
	}
	catalogModels := catalog.Models()
	catalogTemplates := catalog.Templates()
	st := InferenceCatalogState{
		Version:   catalog.Version(),
		Templates: make([]InferenceTemplateView, 0, len(catalogTemplates)),
		Models:    make([]InferenceModelView, 0, len(catalogModels)),
	}
	for _, m := range catalogModels {
		st.Models = append(st.Models, InferenceModelView{
			ID:     m.ID,
			Type:   m.Type,
			Vendor: m.Vendor,
			Label:  m.Label,
			Source: m.Source,
			Model:  m.ModelSpec,
		})
	}
	for _, template := range catalogTemplates {
		models, err := catalog.TemplateModels(template)
		if err != nil {
			return InferenceCatalogState{}, err
		}
		st.Templates = append(st.Templates, InferenceTemplateView{
			ID:       template.ID,
			Label:    template.Label,
			Type:     template.Type,
			Vendor:   template.Vendor,
			API:      template.API,
			Endpoint: template.Endpoint,
			Advanced: template.Advanced,
			Notes:    template.Notes,
			Models:   models,
		})
	}
	return st, nil
}

// ProviderInstanceView is one inference instance as the settings page
// reads it: the canonical row shape (see config.InstanceSpec, which is
// also what a plugin submits) plus the computed flags the page renders.
// The embedded spec keeps the wire JSON flat, and the literal key is
// never part of it.
type ProviderInstanceView struct {
	config.InstanceSpec
	// KeySet reports that the row has a credential.
	KeySet bool `json:"key_set"`
	// KeyEnv reports that the credential is the provider's environment
	// variable.
	KeyEnv bool `json:"key_env"`
	// KeyKeychain reports that the credential lives in the OS
	// credential store.
	KeyKeychain bool `json:"key_keychain"`
	// Managed reports that an installed, enabled plugin owns the row;
	// the settings page shows it read-only.
	Managed bool `json:"managed"`
}

// RouterPolicyView is the router retry policy shown in the settings
// page.
type RouterPolicyView struct {
	MaxAttempts              int  `json:"max_attempts"`
	FallbackOnRetryExhausted bool `json:"fallback_on_retry_exhausted"`
}

// ConfigState is the full inference wiring the settings page edits.
type ConfigState struct {
	Model     string                 `json:"model"`
	Instances []ProviderInstanceView `json:"instances"`
	// Router is the generate retry policy the page edits alongside the
	// instance list.
	Router RouterPolicyView `json:"router"`
}

// ConfigState returns the configured inference wiring plus the current
// default model.
func (b *Config) ConfigState() (ConfigState, error) {
	cfg, err := config.LoadInference(b.core.UserDir)
	if err != nil {
		return ConfigState{}, err
	}
	managed, err := b.managedInstanceIDs()
	if err != nil {
		return ConfigState{}, err
	}
	policy := cfg.Router
	if policy.MaxAttempts <= 0 {
		policy = config.DefaultRouterPolicy()
	}
	st := ConfigState{
		Model: config.DefaultModel(b.core.UserDir),
		Router: RouterPolicyView{
			MaxAttempts:              policy.MaxAttempts,
			FallbackOnRetryExhausted: policy.FallbackOnRetryExhausted,
		},
	}
	for _, in := range cfg.Instances {
		st.Instances = append(st.Instances, ProviderInstanceView{
			InstanceSpec: config.InstanceToSpec(in),
			KeySet: in.KeySource == config.KeyEnv ||
				(in.KeySource == config.KeyLiteral && in.KeyValue != "") ||
				(in.KeySource == config.KeyKeychain && in.KeyValue != ""),
			KeyEnv:      in.KeySource == config.KeyEnv,
			KeyKeychain: in.KeySource == config.KeyKeychain,
			Managed:     managed[in.StableID],
		})
	}
	return st, nil
}

// ModelOption is one selectable per-conversation model hint.

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
			name := strings.TrimSpace(m.Name)
			if name == "" {
				continue
			}
			out = append(out, ModelOption{
				ID:        in.DeploymentID(i+1) + "/" + name,
				Label:     instanceLabelName(in) + " · " + name,
				Reasoning: m.Capabilities.Reasoning.Kind != "",
			})
		}
	}
	return out, nil
}

// instanceLabelName names one instance for the model picker.
func instanceLabelName(in config.Instance) string {
	if in.Name != "" {
		return in.Name
	}
	return in.Type
}

// ModelUsageStat is one model's cumulative user-level usage.
type ModelUsageStat struct {
	Model            string `json:"model"`
	TotalTokens      int64  `json:"total_tokens"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	LatencyMs        int64  `json:"latency_ms"`
	Calls            int64  `json:"calls"`
	Workspaces       int    `json:"workspaces"`
	Sessions         int    `json:"sessions"`
	UpdatedAt        string `json:"updated_at"`
}

// ModelUsage returns per-model token usage.
func (b *Config) ModelUsage() ([]ModelUsageStat, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Usage()
	if store == nil {
		return nil, errNotReady("usage")
	}
	rows, err := store.Summary(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ModelUsageStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModelUsageStat{
			Model:            r.Model,
			TotalTokens:      r.TotalTokens,
			InputTokens:      r.InputTokens,
			OutputTokens:     r.OutputTokens,
			CacheReadTokens:  r.CacheReadTokens,
			CacheWriteTokens: r.CacheWriteTokens,
			ReasoningTokens:  r.ReasoningTokens,
			LatencyMs:        r.LatencyMs,
			Calls:            r.Calls,
			Workspaces:       r.Workspaces,
			Sessions:         r.Sessions,
			UpdatedAt:        r.UpdatedAt,
		})
	}
	return out, nil
}

// ModelUsageSessionCount returns the number of distinct
// (workspace, session) pairs with any recorded usage. One session
// that used several models counts once, unlike the per-model Sessions
// field of ModelUsageStat.
func (b *Config) ModelUsageSessionCount() (int, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Usage()
	if store == nil {
		return 0, errNotReady("usage")
	}
	n, err := store.SessionCount(ctx)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// UsagePoint is one time-bucketed usage sample.
type UsagePoint struct {
	Time             string `json:"time"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
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
	if store == nil {
		return nil, errNotReady("usage")
	}
	if model == "" {
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
			Time:             p.Time,
			InputTokens:      p.InputTokens,
			OutputTokens:     p.OutputTokens,
			CacheReadTokens:  p.CacheReadTokens,
			CacheWriteTokens: p.CacheWriteTokens,
			ReasoningTokens:  p.ReasoningTokens,
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
	return b.core.ApplyDocumentReload(b.core.Shell.Context())
}

// SaveInstances persists inference instances and invalidates pooled
// hosts so the next Acquire rebuilds from the new configuration.
func (b *Config) SaveInstances(req InferenceRequest) error {
	if err := b.saveInference(req); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(b.core.Shell.Context())
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
	return b.core.ApplyDocumentReload(ctx)
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
	defer func() {
		flowtelemetry.WarnErr(ctx, "desktop config: close MCP test source failed",
			src.Close())
	}()
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
			dto.Status = mcpProbeStatus(err)
			if dto.Status == "error" {
				dto.Error = err.Error()
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

// mcpProbeStatus classifies one MCP readiness probe. A probe timeout
// only means the background connect is still running, so it maps to
// "connecting"; terminal failures (validation, rejection, closed
// source) map to "error".
func mcpProbeStatus(err error) string {
	switch {
	case err == nil:
		return "connected"
	case errdefs.IsTimeout(err):
		return "connecting"
	default:
		return "error"
	}
}

// Reload rebuilds the runtime from current configuration.
func (b *Config) Reload() error {
	ctx := b.core.Shell.Context()
	return b.core.ApplyDocumentReload(ctx)
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
	// Instances is the canonical row shape, shared with the plugin
	// write path (config.InstanceSpec); the config layer applies the
	// settings-page source policy when it stores them.
	Instances []config.InstanceSpec `json:"instances"`
	// Router carries the generate retry policy the page edits.
	Router RouterPolicyView `json:"router"`
}

func (b *Config) saveInference(req InferenceRequest) error {
	// Resolve the plugin-owned rows before entering the config-state
	// transaction; managedInstanceIDs reads the owner sidecar and the
	// plugin store, neither of which may be read while holding the
	// inference write lock.
	managed, err := b.managedInstanceIDs()
	if err != nil {
		return err
	}
	policy := config.DefaultRouterPolicy()
	if req.Router.MaxAttempts > 0 {
		policy = config.RouterPolicy{
			MaxAttempts:              req.Router.MaxAttempts,
			FallbackOnRetryExhausted: req.Router.FallbackOnRetryExhausted,
		}
	}
	restored, err := config.ApplySettingsSave(
		b.core.UserDir,
		config.SaveRequest{
			Instances: b.stashLiteralKeys(req.Instances),
			Router:    policy,
		},
		func(stableID string) bool { return managed[stableID] },
	)
	if err != nil {
		return err
	}
	if len(restored) > 0 {
		b.core.Shell.Emit(
			"managed_restored", map[string]any{"ids": restored},
		)
	}
	return nil
}

// stashLiteralKeys moves keys the user typed into the OS credential
// store when it is available and keeps only the account reference in the
// configuration; a failed store write leaves the literal key in the
// 0600 config so the settings page stays usable.
func (b *Config) stashLiteralKeys(
	specs []config.InstanceSpec,
) []config.InstanceSpec {
	if b.core.Plugin == nil || b.core.Plugin.Secrets == nil ||
		!b.core.Plugin.Secrets.Available() {
		return specs
	}
	for i := range specs {
		spec := &specs[i]
		key := strings.TrimSpace(spec.KeyValue)
		if key == "" {
			continue
		}
		if spec.StableID == "" {
			// The row is new; pin its identity before naming the store
			// account so the reference survives the next save.
			spec.StableID = config.NewStableID()
		}
		spec.KeySource = config.KeySourceLiteralName
		account := secrets.AccountFor(config.Instance{
			Type:     strings.TrimSpace(spec.Type),
			StableID: spec.StableID,
		}.DeploymentID(i + 1))
		if err := b.core.Plugin.Secrets.Set(
			b.core.Shell.Context(), account, key,
		); err != nil {
			continue
		}
		spec.KeySource = config.KeySourceKeychainName
		spec.KeyRef = account
		spec.KeyValue = ""
	}
	return specs
}

// managedInstanceIDs returns the stable ids of inference instances
// owned by an installed and enabled plugin. Ownership comes from the
// explicit sidecar. Rows whose plugin is disabled or no longer
// installed are not locked, so the user can remove or edit them after
// disabling a plugin.
func (b *Config) managedInstanceIDs() (map[string]bool, error) {
	if b.core.Plugin == nil || b.core.Plugin.Store == nil {
		return nil, nil
	}
	installed, err := b.core.Plugin.Store.List()
	if err != nil {
		return nil, err
	}
	owners, err := config.LoadProviderOwners(b.core.UserDir)
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]bool, len(installed))
	for _, p := range installed {
		if p.Enabled && p.Error == "" {
			enabled[p.ID] = true
		}
	}
	ids := make(map[string]bool, len(owners))
	for instanceID, pluginID := range owners {
		if enabled[pluginID] {
			ids[instanceID] = true
		}
	}
	return ids, nil
}
