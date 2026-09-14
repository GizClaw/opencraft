package config

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// The user configuration layer is one YAML document: its shape lives in
// a template, and the pieces whose shape depends on the driver (the spec
// body, the driver-specific model fields) arrive pre-rendered. Anything
// that is a decision — which models exist, what kind a model defaults
// to, which keys a driver accepts — stays in Go, where it can be tested
// without parsing YAML.

//go:embed templates/inference.gotmpl
var inferenceTemplates embed.FS

var inferenceTemplate = template.Must(
	template.New("inference").Funcs(template.FuncMap{
		// quote keeps the document's single-quoted style, which users
		// read and edit by hand.
		"quote": yamlQuote,
		// kinds renders one inline YAML list of content kinds.
		"kinds": func(kinds []string) string {
			return "[" + strings.Join(kinds, ", ") + "]"
		},
	}).ParseFS(inferenceTemplates, "templates/inference.gotmpl"),
)

// inferenceView is the document the template renders.
type inferenceView struct {
	Now       string
	Providers []providerView
	Deps      []string
	Router    routerView
}

type providerView struct {
	ID   string
	Impl string
	// Spec is the provider spec body, already indented for its place
	// under `spec:`. Its shape follows the driver, so it is rendered in
	// Go rather than in the template.
	Spec      string
	Models    []modelView
	StableID  string
	Endpoints []endpointView
	APIKey    string
}

type endpointView struct {
	Name     string
	Endpoint string
}

type modelView struct {
	Name          string
	Kind          string
	Outputs       []string
	Inputs        []string
	ReasoningKind string
	EffortMap     []effortView
	WebSearch     bool
	MaxInput      *int
	MaxOutput     *int
	HasLimits     bool
	Lifecycle     *lifecycleView
	// DriverFields is the driver-specific model block, already
	// indented; empty when the model declares none.
	DriverFields string
}

type effortView struct {
	Effort string
	Mode   string
}

type lifecycleView struct {
	Status   string
	Provider string
	Name     string
	Notes    string
}

type routerView struct {
	MaxAttempts int
	Fallback    bool
	Targets     []targetView
}

type targetView struct {
	Provider string
	Name     string
	Profile  string
}

// inferenceYAMLAt renders the user configuration layer with the document
// timestamp injected, so tests can pin the header line.
func (c InferenceConfig) inferenceYAMLAt(now time.Time) ([]byte, error) {
	view, err := c.inferenceView(now)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := inferenceTemplate.ExecuteTemplate(
		&b, "inference", view,
	); err != nil {
		return nil, fmt.Errorf("config: render inference document: %w", err)
	}
	return b.Bytes(), nil
}

// inferenceView validates the configuration and lowers it into the
// template's view model.
func (c InferenceConfig) inferenceView(now time.Time) (inferenceView, error) {
	if len(c.Instances) == 0 {
		return inferenceView{}, errors.New(
			"config: at least one enabled instance is required")
	}
	view := inferenceView{
		Now: now.UTC().Format(time.RFC3339),
		Router: routerView{
			MaxAttempts: c.Router.withDefaults().MaxAttempts,
			Fallback:    c.Router.withDefaults().FallbackOnRetryExhausted,
		},
	}
	// One provider deployment per instance: disabled instances stay
	// declared so re-enabling them needs no re-entry, while only enabled
	// instances join the infer deps and the router targets.
	for i := range c.Instances {
		in := c.Instances[i]
		prov, ok := ProviderFor(in)
		if !ok {
			return inferenceView{}, fmt.Errorf(
				"config: unknown provider type %q", in.Type)
		}
		if err := normalizeModels(&in, prov, i+1); err != nil {
			return inferenceView{}, err
		}
		if strings.TrimSpace(in.Advanced.Routing) == "azure_deployment" &&
			strings.TrimSpace(in.Endpoint) == "" {
			return inferenceView{}, fmt.Errorf(
				"config: instance %d: azure deployment routing needs an endpoint",
				i+1)
		}
		id := in.DeploymentID(i + 1)
		spec, err := providerSpecBlock(prov, in, id)
		if err != nil {
			return inferenceView{}, err
		}
		models := make([]modelView, 0, len(in.Models))
		for _, m := range in.Models {
			model, err := modelViewFor(m, prov)
			if err != nil {
				return inferenceView{}, err
			}
			models = append(models, model)
		}
		view.Providers = append(view.Providers, providerView{
			ID:        id,
			Impl:      prov.Impl,
			Spec:      spec,
			Models:    models,
			StableID:  in.StableID,
			Endpoints: providerEndpoints(in, prov),
			APIKey:    instanceAPIKey(in, prov),
		})
		// Router targets: one per model of an enabled instance, in
		// priority order.
		if in.Enabled {
			view.Deps = append(view.Deps, id)
			for _, m := range in.Models {
				view.Router.Targets = append(view.Router.Targets, targetView{
					Provider: id,
					Name:     m.Name,
					Profile:  in.StableID,
				})
			}
		}
	}
	return view, nil
}

// providerSpecBlock renders one provider's spec body: the basic endpoint
// plus every advanced knob, then the opaque provider-spec bag with the
// keys the writer already emitted removed.
func providerSpecBlock(prov Provider, in Instance, id string) (string, error) {
	var b strings.Builder
	written, err := writeProviderSpec(
		&b, prov, in, strings.TrimSpace(in.API),
	)
	if err != nil {
		return "", err
	}
	if len(in.ProviderSpec) > 0 {
		if err := writeProviderSpecYAML(
			&b, compat.NormalizeProviderSpec(in.ProviderSpec), written,
		); err != nil {
			return "", fmt.Errorf(
				"config: encode provider spec for %s: %w", id, err)
		}
	}
	return b.String(), nil
}

// providerEndpoints lists the per-model deployment addresses one profile
// binds. Only drivers that address models per endpoint (ByteDance Ark
// ep-xxx ids are account-scoped) carry them.
func providerEndpoints(in Instance, prov Provider) []endpointView {
	if prov.Impl != "bytedance" {
		return nil
	}
	var out []endpointView
	for _, m := range in.Models {
		if strings.TrimSpace(m.Endpoint) == "" {
			continue
		}
		out = append(out, endpointView{Name: m.Name, Endpoint: m.Endpoint})
	}
	return out
}

// modelViewFor lowers one declared model into its template view: the
// family defaults (a generate model always states text output), the
// ordered effort ladder, and the driver-specific block.
func modelViewFor(m Model, prov Provider) (modelView, error) {
	kind := m.Kind
	outputs := m.Capabilities.Outputs
	if kind == "" {
		switch {
		case slices.Contains(outputs, message.PartVideo):
			kind = "video"
		case slices.Contains(outputs, message.PartImage):
			kind = "image"
		default:
			kind = "generate"
		}
	}
	if len(outputs) == 0 && kind == "generate" {
		// Text output is the generate family default; a declared model
		// without it would fail driver validation.
		outputs = []message.PartKind{message.PartText}
	}
	// Undeclared inputs stay undeclared: flowcraft reads an empty input
	// list as "unknown" and filters nothing, while a declared list is
	// enforced before any provider opens.
	view := modelView{
		Name:          m.Name,
		Kind:          kind,
		Outputs:       PartKindStrings(outputs),
		Inputs:        PartKindStrings(m.Capabilities.Inputs),
		WebSearch:     m.Capabilities.HostedWebSearch,
		MaxInput:      m.Limits.MaxInputTokens,
		MaxOutput:     m.Limits.MaxOutputTokens,
		HasLimits:     m.Limits.MaxInputTokens != nil || m.Limits.MaxOutputTokens != nil,
		ReasoningKind: string(m.Capabilities.Reasoning.Kind),
	}
	for _, effort := range reasoningEffortOrder {
		mode, ok := m.Capabilities.Reasoning.EffortMap[effort]
		if !ok {
			continue
		}
		view.EffortMap = append(view.EffortMap, effortView{
			Effort: string(effort),
			Mode:   mode,
		})
	}
	lifecycle, err := lifecycleViewFor(m.Name, m.Lifecycle)
	if err != nil {
		return modelView{}, err
	}
	view.Lifecycle = lifecycle
	fields, err := modelDriverFieldsBlock(m.Name, prov.Impl, m.DriverFields)
	if err != nil {
		return modelView{}, err
	}
	view.DriverFields = fields
	return view, nil
}

// lifecycleViewFor validates and lowers one model's discovery metadata.
// An active model must not carry retirement facts, which is why an empty
// status drops the block entirely.
func lifecycleViewFor(
	modelName string, lifecycle ModelLifecycle,
) (*lifecycleView, error) {
	if lifecycle.IsZero() {
		return nil, nil
	}
	status := strings.TrimSpace(lifecycle.Status)
	switch status {
	case "deprecated", "retired":
	case "":
		status = "deprecated"
	default:
		return nil, fmt.Errorf(
			"config: model %s lifecycle status %q must be deprecated or retired",
			modelName, status,
		)
	}
	view := &lifecycleView{
		Status: status,
		Notes:  strings.TrimSpace(lifecycle.Notes),
	}
	view.Name = strings.TrimSpace(lifecycle.ReplacementName)
	if view.Name != "" {
		view.Provider = strings.TrimSpace(lifecycle.ReplacementProvider)
		if view.Provider == "" {
			return nil, fmt.Errorf(
				"config: model lifecycle replacement %q needs a provider",
				view.Name,
			)
		}
	}
	return view, nil
}

// modelDriverFieldsBlock renders the driver-specific leaves of one model
// entry, already indented.
func modelDriverFieldsBlock(
	modelName, impl string, fields map[string]any,
) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	var b strings.Builder
	if err := writeModelDriverFields(&b, modelName, impl, fields); err != nil {
		return "", err
	}
	return b.String(), nil
}
