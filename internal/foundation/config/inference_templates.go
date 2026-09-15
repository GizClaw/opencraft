package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Built-in inference templates.
//
// The runtime deliberately keeps no vendor table: which endpoint and
// which models a deployment serves is deployment data, written per
// instance by the settings page. This catalog is the prefill layer on
// top of that decision, not a second source of deployments: nothing here
// reaches a deployment until the user adds an entry, every value it
// fills stays editable, and the settings-page save path validates it
// exactly like a hand-typed row (see ParseInferenceCatalog).
//
// The document is embedded as data (assets/inference_templates.json)
// rather than written as Go literals so it stays reviewable, scriptable,
// and shaped like the catalog a later remote update would carry. Each
// model entry names where its facts came from; keeping that honest is
// what makes a stale entry findable.

// inferenceCatalogAsset is the embedded catalog path.
const inferenceCatalogAsset = "assets/inference_templates.json"

// InferenceCatalogModel is one built-in model declaration. Type names
// the provider catalog entry (config.Providers) that serves it, so a
// model can never point at a driver the deployment cannot build.
type InferenceCatalogModel struct {
	// ID is the catalog key templates reference ("<vendor>/<name>" by
	// convention).
	ID string `json:"id"`
	// Type is the provider type from the inference provider catalog.
	Type string `json:"type"`
	// Vendor is the brand the model belongs to. It only groups and
	// labels entries: several vendors share the OpenAI wire family.
	Vendor string `json:"vendor,omitempty"`
	// Label is the display name the settings page shows.
	Label string `json:"label,omitempty"`
	// Source records where the facts came from (documentation, a
	// fixture, a changelog entry). It is maintenance metadata, never
	// shown as configuration.
	Source string `json:"source,omitempty"`
	// ModelSpec carries the declaration itself, in the same shape the
	// settings page submits and a plugin upserts.
	ModelSpec
}

// InferenceCatalogTemplate is one starter instance: the provider-level
// fields plus the catalog models the new row starts with, in router
// priority order.
type InferenceCatalogTemplate struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Type   string `json:"type"`
	Vendor string `json:"vendor,omitempty"`
	// API is the wire surface the deployment uses (responses | chat);
	// empty keeps the driver default.
	API string `json:"api,omitempty"`
	// Endpoint is the base URL the deployment starts with; empty uses
	// the driver default.
	Endpoint string `json:"endpoint,omitempty"`
	// Advanced carries the provider-level knobs the template pins, in
	// the same typed shape the settings page edits. It exists because a
	// model declaration can depend on an endpoint fact: a chat
	// deployment that serves video input states it here, and the driver
	// rejects the model without it.
	Advanced InstanceAdvanced `json:"advanced,omitempty"`
	// Notes is maintenance guidance for the entry, not user-facing copy.
	Notes string `json:"notes,omitempty"`
	// Models lists InferenceCatalogModel ids.
	Models []string `json:"models"`
}

// InferenceCatalog is one parsed and validated catalog document.
type InferenceCatalog struct {
	version   string
	notes     string
	models    []InferenceCatalogModel
	templates []InferenceCatalogTemplate
	byID      map[string]InferenceCatalogModel
}

// inferenceCatalogDoc is the on-disk document shape.
type inferenceCatalogDoc struct {
	Version   string                     `json:"version"`
	Notes     string                     `json:"notes,omitempty"`
	Models    []InferenceCatalogModel    `json:"models"`
	Templates []InferenceCatalogTemplate `json:"templates"`
}

// Version returns the catalog revision ("2026.09.15" style). A later
// remote catalog compares versions to decide whether an update is older.
func (c InferenceCatalog) Version() string { return c.version }

// Notes returns the document-level maintenance note.
func (c InferenceCatalog) Notes() string { return c.notes }

// Models returns the model entries in document order.
func (c InferenceCatalog) Models() []InferenceCatalogModel {
	return append([]InferenceCatalogModel(nil), c.models...)
}

// Templates returns the instance templates in document order.
func (c InferenceCatalog) Templates() []InferenceCatalogTemplate {
	return append([]InferenceCatalogTemplate(nil), c.templates...)
}

// Model resolves one catalog model id.
func (c InferenceCatalog) Model(id string) (InferenceCatalogModel, bool) {
	m, ok := c.byID[id]
	return m, ok
}

// TemplateModels resolves one template into the wire shape the settings
// page submits. Parse validated every reference, so the error only
// fires for a zero catalog.
func (c InferenceCatalog) TemplateModels(t InferenceCatalogTemplate) ([]ModelSpec, error) {
	if len(t.Models) == 0 {
		return nil, fmt.Errorf(
			"config: inference template %s declares no models", t.ID,
		)
	}
	out := make([]ModelSpec, 0, len(t.Models))
	for _, id := range t.Models {
		m, ok := c.byID[id]
		if !ok {
			return nil, fmt.Errorf(
				"config: inference template %s references unknown model %q",
				t.ID, id,
			)
		}
		out = append(out, m.ModelSpec)
	}
	return out, nil
}

// loadInferenceCatalog reads and parses the embedded catalog once.
var loadInferenceCatalog = sync.OnceValues(func() (InferenceCatalog, error) {
	data, err := assets.ReadFile(inferenceCatalogAsset)
	if err != nil {
		return InferenceCatalog{}, fmt.Errorf(
			"config: read built-in inference templates: %w", err,
		)
	}
	return ParseInferenceCatalog(data)
})

// LoadInferenceCatalog returns the embedded built-in catalog. The
// document is parsed and validated once per process.
func LoadInferenceCatalog() (InferenceCatalog, error) {
	return loadInferenceCatalog()
}

// ParseInferenceCatalog decodes one catalog document and validates every
// entry through the same path a settings-page save takes, so a shipped
// entry cannot declare something the writer would reject. Unknown fields
// are errors: a typo in the document must fail loudly instead of
// silently dropping a fact.
func ParseInferenceCatalog(data []byte) (InferenceCatalog, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc inferenceCatalogDoc
	if err := dec.Decode(&doc); err != nil {
		return InferenceCatalog{}, fmt.Errorf(
			"config: decode inference catalog: %w", err,
		)
	}
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return InferenceCatalog{}, errors.New(
			"config: inference catalog carries trailing content",
		)
	}
	catalog := InferenceCatalog{
		version: strings.TrimSpace(doc.Version),
		notes:   strings.TrimSpace(doc.Notes),
		byID:    make(map[string]InferenceCatalogModel, len(doc.Models)),
	}
	if catalog.version == "" {
		return InferenceCatalog{}, errors.New(
			"config: inference catalog needs a version",
		)
	}
	for _, model := range doc.Models {
		model.ID = strings.TrimSpace(model.ID)
		model.Type = strings.TrimSpace(model.Type)
		model.Vendor = strings.TrimSpace(model.Vendor)
		model.Label = strings.TrimSpace(model.Label)
		model.Source = strings.TrimSpace(model.Source)
		if model.ID == "" {
			return InferenceCatalog{}, errors.New(
				"config: inference catalog model needs an id",
			)
		}
		if _, dup := catalog.byID[model.ID]; dup {
			return InferenceCatalog{}, fmt.Errorf(
				"config: inference catalog model %s is declared twice", model.ID,
			)
		}
		prov, ok := ProviderByID(model.Type)
		if !ok {
			return InferenceCatalog{}, fmt.Errorf(
				"config: inference catalog model %s names unknown provider type %q",
				model.ID, model.Type,
			)
		}
		if err := validateCatalogModel(model, prov); err != nil {
			return InferenceCatalog{}, err
		}
		catalog.byID[model.ID] = model
		catalog.models = append(catalog.models, model)
	}
	if len(catalog.models) == 0 {
		return InferenceCatalog{}, errors.New(
			"config: inference catalog declares no models",
		)
	}
	seen := make(map[string]bool, len(doc.Templates))
	for _, template := range doc.Templates {
		template.ID = strings.TrimSpace(template.ID)
		template.Label = strings.TrimSpace(template.Label)
		template.Type = strings.TrimSpace(template.Type)
		template.Vendor = strings.TrimSpace(template.Vendor)
		template.API = strings.TrimSpace(template.API)
		template.Endpoint = strings.TrimSpace(template.Endpoint)
		template.Advanced = template.Advanced.normalized()
		template.Notes = strings.TrimSpace(template.Notes)
		if template.ID == "" {
			return InferenceCatalog{}, errors.New(
				"config: inference catalog template needs an id",
			)
		}
		if seen[template.ID] {
			return InferenceCatalog{}, fmt.Errorf(
				"config: inference catalog template %s is declared twice",
				template.ID,
			)
		}
		seen[template.ID] = true
		if _, ok := ProviderByID(template.Type); !ok {
			return InferenceCatalog{}, fmt.Errorf(
				"config: inference catalog template %s names unknown provider "+
					"type %q", template.ID, template.Type,
			)
		}
		if len(template.Models) == 0 {
			return InferenceCatalog{}, fmt.Errorf(
				"config: inference catalog template %s declares no models",
				template.ID,
			)
		}
		for _, id := range template.Models {
			if _, ok := catalog.byID[id]; !ok {
				return InferenceCatalog{}, fmt.Errorf(
					"config: inference catalog template %s references unknown "+
						"model %q", template.ID, id,
				)
			}
		}
		// Validate the provider-level fields the page will prefill too,
		// without a credential source: templates never carry one.
		resolved, err := catalog.TemplateModels(template)
		if err != nil {
			return InferenceCatalog{}, err
		}
		if _, err := (InstanceSpec{
			Type:     template.Type,
			API:      template.API,
			Endpoint: template.Endpoint,
			Advanced: template.Advanced,
			Models:   resolved,
		}).Lower(SourceUser, ""); err != nil {
			return InferenceCatalog{}, fmt.Errorf(
				"config: inference catalog template %s: %w", template.ID, err,
			)
		}
		catalog.templates = append(catalog.templates, template)
	}
	if len(catalog.templates) == 0 {
		return InferenceCatalog{}, errors.New(
			"config: inference catalog declares no templates",
		)
	}
	return catalog, nil
}

// validateCatalogModel runs one entry through the settings-page write
// path: lowering the row, normalizing the model list, and lowering the
// model view (which validates lifecycle metadata and the driver-field
// allowlist of the driver that serves it).
func validateCatalogModel(model InferenceCatalogModel, prov Provider) error {
	spec := model.ModelSpec
	spec.Name = strings.TrimSpace(spec.Name)
	in, err := InstanceSpec{
		Type:   model.Type,
		Models: []ModelSpec{spec},
	}.Lower(SourceUser, "")
	if err != nil {
		return fmt.Errorf("config: inference catalog model %s: %w", model.ID, err)
	}
	if err := normalizeModels(&in, prov, 1); err != nil {
		return fmt.Errorf("config: inference catalog model %s: %w", model.ID, err)
	}
	if _, err := modelViewFor(in.Models[0], prov); err != nil {
		return fmt.Errorf("config: inference catalog model %s: %w", model.ID, err)
	}
	return nil
}
