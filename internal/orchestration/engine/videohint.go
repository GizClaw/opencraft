package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/message"
)

// videoHints lists the router hints whose model can take a video part
// on this deployment, keyed the way a turn names its model
// ("<deployment-id>/<name>"). The media prepare hook reads the list
// from its settings: a deployment whose endpoint or model has no video
// input *rejects* a video part instead of ignoring it, so the hook has
// to keep flattening video to its path everywhere else.
//
// A provider contributes a model only when both say yes: the model
// declares video input, and the provider's wire accepts video blocks
// (flowcraft's `wire.video_input`, which the OpenAI wire family gates
// to the chat surface and Anthropic exposes for compatible endpoints).
// The entry for the router's default target is stored as "" so the hook
// can answer turns that carry no model hint.
func videoHints(doc deploy.Document) []string {
	var hints []string
	for id, res := range doc.Resources {
		if res.Kind != "inference.Provider" {
			continue
		}
		names := videoModels(res.Settings)
		if len(names) == 0 {
			continue
		}
		deployment := providerDeploymentID(id, res.Settings)
		if deployment == "" {
			continue
		}
		for _, name := range names {
			hints = append(hints, deployment+"/"+name)
		}
	}
	// A turn with no model hint resolves to the router's first target,
	// so record that capability under "" as well.
	defaultHint := routerDefaultHint(doc)
	if slices.Contains(hints, defaultHint) {
		hints = append([]string{""}, hints...)
	}
	return hints
}

// videoModels returns the model names one provider resource declares
// with video input, or nil when the provider's wire cannot carry video
// at all.
func videoModels(settings json.RawMessage) []string {
	var doc struct {
		Spec struct {
			API  string `json:"api"`
			Wire struct {
				VideoInput bool `json:"video_input"`
			} `json:"wire"`
			Models []struct {
				Name         string `json:"name"`
				Capabilities struct {
					Inputs []string `json:"inputs"`
				} `json:"capabilities"`
			} `json:"models"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(settings, &doc); err != nil {
		return nil
	}
	if !doc.Spec.Wire.VideoInput {
		return nil
	}
	// The OpenAI wire family lowers video only on the chat surface
	// (Responses has no video content part); Anthropic has a single
	// surface, so an empty api is fine there.
	if doc.Spec.API != "" && doc.Spec.API != "chat" {
		return nil
	}
	var names []string
	for _, model := range doc.Spec.Models {
		for _, input := range model.Capabilities.Inputs {
			if message.PartKind(input) == message.PartVideo {
				names = append(names, model.Name)
				break
			}
		}
	}
	return names
}

// providerDeploymentID reads the deployment id one provider resource
// routerDefaultHint returns the hint of the router's first generate
// target — the model a turn with no explicit hint resolves to.
func routerDefaultHint(doc deploy.Document) string {
	res, ok := doc.Resources["router"]
	if !ok || len(res.Settings) == 0 {
		return ""
	}
	var policy struct {
		Generate []struct {
			Targets []struct {
				Model struct {
					ID struct {
						Provider string `json:"provider"`
						Name     string `json:"name"`
					} `json:"id"`
				} `json:"model"`
			} `json:"targets"`
		} `json:"generate"`
	}
	if err := json.Unmarshal(res.Settings, &policy); err != nil {
		return ""
	}
	for _, pool := range policy.Generate {
		for _, target := range pool.Targets {
			id := target.Model.ID
			if id.Provider == "" || id.Name == "" {
				continue
			}
			return id.Provider + "/" + id.Name
		}
	}
	return ""
}

// providerDeploymentID reads the deployment id one provider resource
// serves ("provider.<id>" unless settings.id overrides it).
func providerDeploymentID(resourceID string, settings json.RawMessage) string {
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(settings, &doc); err == nil && doc.ID != "" {
		return doc.ID
	}
	return strings.TrimPrefix(resourceID, "provider.")
}

// withVideoHints returns the document with the media prepare hook's
// video list filled in.
func withVideoHints(doc deploy.Document) (deploy.Document, error) {
	hints := videoHints(doc)
	if len(hints) == 0 {
		return doc, nil
	}
	for id, res := range doc.Resources {
		if res.Kind != "hook.prepare" || res.Impl != "opencraft.media" {
			continue
		}
		settings := map[string]any{}
		if len(res.Settings) > 0 {
			if err := json.Unmarshal(res.Settings, &settings); err != nil {
				return doc, fmt.Errorf(
					"engine: decode %s settings: %w", id, err)
			}
		}
		settings["video_models"] = hints
		raw, err := json.Marshal(settings)
		if err != nil {
			return doc, fmt.Errorf("engine: encode %s settings: %w", id, err)
		}
		res.Settings = raw
		doc.Resources[id] = res
	}
	return doc, nil
}
