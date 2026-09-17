package bindings

import (
	"fmt"
	"strings"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Provider-specific tool options as the settings page consumes them:
// the host owns the vocabulary (foundation/config), this layer maps it
// into DTOs, and the page renders one card per generation tool.

// ToolOptionFieldView is one configurable knob of a provider.
type ToolOptionFieldView struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Values []string `json:"values,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	// ExclusiveMin/Max mark bounds the value must stay strictly within.
	ExclusiveMin bool `json:"exclusive_min,omitempty"`
	ExclusiveMax bool `json:"exclusive_max,omitempty"`
}

// ToolOptionInstanceView is one deployment a card can configure. Id is
// the deployment id the options are keyed by; a plugin-declared row is
// Managed, which the card shows but still lets the user configure,
// because the options live outside the plugin-owned instance.
type ToolOptionInstanceView struct {
	ID      string                `json:"id"`
	Label   string                `json:"label"`
	Impl    string                `json:"impl"`
	Managed bool                  `json:"managed"`
	Fields  []ToolOptionFieldView `json:"fields"`
	// Values are the configured knob values keyed by dotted field path.
	Values map[string]any `json:"values"`
}

// ToolOptionsToolView is one generation tool's card.
type ToolOptionsToolView struct {
	Instances []ToolOptionInstanceView `json:"instances"`
}

// ToolOptionsState is the whole state of the tools settings tab.
type ToolOptionsState struct {
	Image ToolOptionsToolView `json:"image"`
	Video ToolOptionsToolView `json:"video"`
}

// ToolOptionsRequest is the save payload: the flat dotted values the
// page edited, per tool and deployment id.
type ToolOptionsRequest struct {
	Image map[string]map[string]any `json:"image"`
	Video map[string]map[string]any `json:"video"`
}

// ToolOptions returns the provider-specific knobs each generation tool
// can configure, together with the values already stored.
func (b *Config) ToolOptions() (ToolOptionsState, error) {
	cfg, err := config.LoadInference(b.core.UserDir)
	if err != nil {
		return ToolOptionsState{}, err
	}
	stored, err := config.LoadToolOptions(b.core.UserDir)
	if err != nil {
		return ToolOptionsState{}, err
	}
	managed, err := b.managedInstanceIDs()
	if err != nil {
		return ToolOptionsState{}, err
	}
	var out ToolOptionsState
	for i, in := range cfg.Instances {
		if !in.Enabled {
			continue
		}
		prov, ok := config.ProviderFor(in)
		if !ok {
			continue
		}
		id := in.DeploymentID(i + 1)
		var image, video bool
		for _, model := range in.Models {
			image = image || model.ServesImage()
			video = video || model.ServesVideo()
		}
		if image {
			out.Image.Instances = append(out.Image.Instances,
				toolOptionInstance(id, in, prov, managed[id],
					config.ToolImage, stored.Image[id]))
		}
		if video {
			out.Video.Instances = append(out.Video.Instances,
				toolOptionInstance(id, in, prov, managed[id],
					config.ToolVideo, stored.Video[id]))
		}
	}
	return out, nil
}

// toolOptionInstance renders one card row: the driver's vocabulary plus
// the values already configured for it.
func toolOptionInstance(
	id string,
	in config.Instance,
	prov config.Provider,
	managed bool,
	tool string,
	values map[string]any,
) ToolOptionInstanceView {
	schema := config.ToolOptionSchema(tool, prov.Impl)
	fields := make([]ToolOptionFieldView, 0, len(schema))
	for _, field := range schema {
		fields = append(fields, ToolOptionFieldView{
			Name:         field.Name,
			Kind:         string(field.Kind),
			Values:       field.Values,
			Min:          field.Min,
			Max:          field.Max,
			ExclusiveMin: field.ExclusiveMin,
			ExclusiveMax: field.ExclusiveMax,
		})
	}
	return ToolOptionInstanceView{
		ID:      id,
		Label:   toolOptionInstanceLabel(in),
		Impl:    prov.Impl,
		Managed: managed,
		Fields:  fields,
		Values:  config.FlattenToolOptions(schema, values),
	}
}

// toolOptionInstanceLabel names one deployment the way the inference
// settings list does: the provider's display name (or its type, with
// the driver for a plugin-declared vendor), plus a user-typed name.
func toolOptionInstanceLabel(in config.Instance) string {
	label := in.Type
	if preset, ok := config.ProviderByID(in.Type); ok && preset.Name != "" {
		label = preset.Name
	} else if strings.TrimSpace(in.Driver) != "" {
		label = in.Type + " (" + in.Driver + ")"
	}
	if strings.TrimSpace(in.Name) != "" {
		label += " · " + in.Name
	}
	return label
}

// SaveToolOptions validates the submitted knobs against the configured
// instances, stores them in the user layer, and reloads the runtime so
// the tools pick them up.
func (b *Config) SaveToolOptions(req ToolOptionsRequest) error {
	cfg, err := config.LoadInference(b.core.UserDir)
	if err != nil {
		return err
	}
	opts := config.ToolOptions{}
	if opts.Image, err = nestToolOptions(cfg, config.ToolImage, req.Image); err != nil {
		return err
	}
	if opts.Video, err = nestToolOptions(cfg, config.ToolVideo, req.Video); err != nil {
		return err
	}
	if err := config.SaveToolOptions(b.core.UserDir, opts, cfg.Instances); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(b.core.Shell.Context())
}

// nestToolOptions turns the flat dotted values the page submits back
// into the nested objects the drivers decode, resolving each id through
// the instance's driver vocabulary.
func nestToolOptions(
	cfg config.InferenceConfig, tool string, flat map[string]map[string]any,
) (map[string]map[string]any, error) {
	if len(flat) == 0 {
		return nil, nil
	}
	known := make(map[string]config.Instance, len(cfg.Instances))
	for i, in := range cfg.Instances {
		known[in.DeploymentID(i+1)] = in
	}
	out := make(map[string]map[string]any, len(flat))
	for id, values := range flat {
		in, ok := known[id]
		if !ok {
			return nil, fmt.Errorf("tool options: unknown provider %q", id)
		}
		prov, ok := config.ProviderFor(in)
		if !ok {
			return nil, fmt.Errorf("tool options: provider %q has no driver", id)
		}
		schema := config.ToolOptionSchema(tool, prov.Impl)
		if len(schema) == 0 {
			return nil, fmt.Errorf(
				"tool options: provider %s does not support %s-specific options",
				id, tool)
		}
		nested, err := config.NestToolOptions(schema, values)
		if err != nil {
			return nil, fmt.Errorf("tool options: provider %s: %w", id, err)
		}
		if len(nested) > 0 {
			out[id] = nested
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
