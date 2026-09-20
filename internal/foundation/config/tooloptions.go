package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/message"
	"sigs.k8s.io/yaml"
)

// Provider-specific tool options. The model-facing tool schema stays
// canonical; the knobs a driver models beyond that (OpenAI's image
// editing knobs, Seedream's size tokens, the video task settings) are
// deployment configuration the settings page writes into the user layer
// under resources.tool.imagegen / resources.tool.videogen, and the tools
// attach to the provider they route to.

// Tool option groups, one per generation tool.
const (
	ToolImage = "image"
	ToolVideo = "video"
)

// toolResourceKeys maps a tool group to the deploy resource that carries
// its settings.
var toolResourceKeys = map[string]string{
	ToolImage: "tool.imagegen",
	ToolVideo: "tool.videogen",
}

// ToolOptionKind is the control the settings page renders for a field.
type ToolOptionKind string

const (
	ToolOptionBool   ToolOptionKind = "bool"
	ToolOptionInt    ToolOptionKind = "int"
	ToolOptionFloat  ToolOptionKind = "float"
	ToolOptionEnum   ToolOptionKind = "enum"
	ToolOptionString ToolOptionKind = "string"
)

// ToolOptionField is one provider-specific knob. Name is the path inside
// the provider's option object, dots separating nesting
// ("optimize_prompt.mode"), and matches the driver's JSON tags exactly.
type ToolOptionField struct {
	Name   string
	Kind   ToolOptionKind
	Values []string
	Min    *float64
	Max    *float64
	// ExclusiveMin/ExclusiveMax mark bounds the value must stay strictly
	// within (a positive guidance scale, say).
	ExclusiveMin bool
	ExclusiveMax bool
	// Default is the provider-side default the driver documents for this
	// knob, for display only: leaving the knob unset is what keeps the
	// provider in charge of it. Empty means the driver does not state
	// one, and the page falls back to "follows the provider default".
	Default string
}

func enumField(name string, values ...string) ToolOptionField {
	return ToolOptionField{Name: name, Kind: ToolOptionEnum, Values: values}
}

func intField(name string, min, max int) ToolOptionField {
	lo, hi := float64(min), float64(max)
	return ToolOptionField{Name: name, Kind: ToolOptionInt, Min: &lo, Max: &hi}
}

func boolField(name string) ToolOptionField {
	return ToolOptionField{Name: name, Kind: ToolOptionBool}
}

func stringField(name string) ToolOptionField {
	return ToolOptionField{Name: name, Kind: ToolOptionString}
}

// positiveFloatField is an open (0, ∞) numeric knob.
func positiveFloatField(name string) ToolOptionField {
	lo := 0.0
	return ToolOptionField{
		Name: name, Kind: ToolOptionFloat, Min: &lo, ExclusiveMin: true,
	}
}

// withDefault records the provider default a driver documents, so the
// settings page can show what an unset knob resolves to instead of
// guessing a value into the user layer.
func withDefault(field ToolOptionField, value any) ToolOptionField {
	field.Default = fmt.Sprint(value)
	return field
}

// toolOptionFields is the driver vocabulary per tool and driver impl,
// mirroring the image_options / video_options structs of the drivers
// the host registers. The driver decoders stay the final authority: a
// settings save validates through them when a runtime is available, and
// a field this table no longer matches fails the save loudly.
var toolOptionFields = map[string]map[string][]ToolOptionField{
	ToolImage: {
		"openai": {
			withDefault(
				enumField("background", "auto", "opaque", "transparent"),
				"auto",
			),
			withDefault(intField("output_compression", 0, 100), 100),
			withDefault(enumField("input_fidelity", "low", "high"), "low"),
			withDefault(enumField("moderation", "auto", "low"), "auto"),
		},
		"bytedance": {
			positiveFloatField("guidance_scale"),
			boolField("watermark"),
			enumField("optimize_prompt.mode", "standard", "fast"),
			enumField("optimize_prompt.thinking", "auto", "enabled", "disabled"),
			boolField("sequential"),
			intField("sequential_max_images", 1, 15),
			enumField("size_token", "1k", "1.5k", "2k", "3k", "4k", "adaptive"),
			boolField("web_search"),
			boolField("layer_decomposition"),
			enumField("background", "transparent", "opaque"),
		},
		"minimax": {
			enumField(
				"aspect_ratio",
				"1:1", "16:9", "4:3", "3:2", "2:3", "3:4", "9:16", "21:9",
			),
		},
	},
	ToolVideo: {
		"bytedance": {
			boolField("camera_fixed"),
			boolField("generate_audio"),
			enumField("service_tier", "default", "flex"),
			withDefault(
				intField("execution_expires_after", 3600, 259200), 172800,
			),
			intField("priority", 0, 9),
			withDefault(enumField("output_format", "mp4", "mov"), "mp4"),
			withDefault(
				enumField(
					"omni_reference_task_type",
					"auto", "reference", "edit", "extend",
				),
				"auto",
			),
			boolField("web_search"),
			stringField("callback_url"),
			stringField("safety_identifier"),
		},
		"minimax": {
			stringField("callback_url"),
			withDefault(boolField("prompt_optimizer"), true),
			boolField("fast_pretreatment"),
		},
	},
}

// ToolOptionPreset is a named, user-invoked starting point for one
// provider: the page fills these knobs into the form and nothing is
// written until the user saves, so a recommendation never pins the
// provider's own default. Fields are dotted vocabulary paths; ID is the
// page's translation suffix (config.toolPreset.<id>).
type ToolOptionPreset struct {
	ID     string
	Fields map[string]any
}

// toolOptionPresets are the shortcuts the settings page offers per
// driver: values a user commonly wants that the provider does not
// default to. Every entry must stay inside toolOptionFields above —
// TestToolOptionPresetsMatchVocabulary pins that.
var toolOptionPresets = map[string]map[string][]ToolOptionPreset{
	ToolImage: {
		"openai": {
			{
				ID:     "transparent_background",
				Fields: map[string]any{"background": "transparent"},
			},
			{
				ID:     "edit_fidelity",
				Fields: map[string]any{"input_fidelity": "high"},
			},
		},
		"bytedance": {
			{
				ID:     "no_watermark",
				Fields: map[string]any{"watermark": false},
			},
			{
				ID:     "size_2k",
				Fields: map[string]any{"size_token": "2k"},
			},
			{
				ID:     "optimize_standard",
				Fields: map[string]any{"optimize_prompt.mode": "standard"},
			},
		},
		"minimax": {
			{
				ID:     "portrait_9_16",
				Fields: map[string]any{"aspect_ratio": "9:16"},
			},
		},
	},
	ToolVideo: {
		"bytedance": {
			{
				ID:     "with_audio",
				Fields: map[string]any{"generate_audio": true},
			},
			{
				ID:     "flex_tier",
				Fields: map[string]any{"service_tier": "flex"},
			},
			{
				ID:     "camera_fixed",
				Fields: map[string]any{"camera_fixed": true},
			},
		},
		"minimax": {
			{
				ID:     "fast_pretreatment",
				Fields: map[string]any{"fast_pretreatment": true},
			},
			{
				ID:     "no_prompt_optimizer",
				Fields: map[string]any{"prompt_optimizer": false},
			},
		},
	},
}

// ToolOptionSchema returns the provider-specific fields one tool offers
// for a driver impl, in declaration order. An unknown impl has none.
func ToolOptionSchema(tool, impl string) []ToolOptionField {
	byImpl, ok := toolOptionFields[tool]
	if !ok {
		return nil
	}
	return byImpl[impl]
}

// ToolOptionPresets returns the presets one tool offers for a driver
// impl, in declaration order. An unknown impl has none.
func ToolOptionPresets(tool, impl string) []ToolOptionPreset {
	byImpl, ok := toolOptionPresets[tool]
	if !ok {
		return nil
	}
	return byImpl[impl]
}

// ToolOptions is the configured knob set: deployment id -> the option
// object handed to that provider's extension (driver-native shape, so
// nested objects stay nested on disk and on the wire).
type ToolOptions struct {
	Image map[string]map[string]any `json:"image,omitempty"`
	Video map[string]map[string]any `json:"video,omitempty"`
}

// optionsFor returns the knob set of one tool group.
func (o ToolOptions) optionsFor(tool string) map[string]map[string]any {
	if tool == ToolVideo {
		return o.Video
	}
	return o.Image
}

// PruneToolOptions drops the knob blocks whose deployment id is no
// longer a configured instance. Blocks are keyed by deployment id, so a
// removed provider would otherwise leave its knobs behind: invisible in
// the settings page (which lists configured instances), still sitting in
// the user layer, and silently inherited by a later provider that
// re-uses the id. Disabled instances keep their knobs — the row is still
// declared, so re-enabling it must not lose the configuration.
//
// dropped reports whether anything was removed.
func PruneToolOptions(
	opts ToolOptions, instances []Instance,
) (ToolOptions, bool) {
	alive := make(map[string]bool, len(instances))
	for i, in := range instances {
		alive[in.DeploymentID(i+1)] = true
	}
	dropped := false
	opts.Image = pruneToolOptionBlocks(opts.Image, alive, &dropped)
	opts.Video = pruneToolOptionBlocks(opts.Video, alive, &dropped)
	return opts, dropped
}

// pruneToolOptionBlocks keeps the blocks whose id is still alive, or
// nil when nothing is left.
func pruneToolOptionBlocks(
	blocks map[string]map[string]any, alive map[string]bool, dropped *bool,
) map[string]map[string]any {
	if len(blocks) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(blocks))
	for id, values := range blocks {
		if !alive[id] {
			*dropped = true
			continue
		}
		out[id] = values
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadToolOptions reads the configured options out of the user layer.
// A missing file or section is an empty configuration, not an error.
func LoadToolOptions(configDir string) (ToolOptions, error) {
	var out ToolOptions
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("config: read user layer: %w", err)
	}
	var doc struct {
		Resources map[string]struct {
			Settings struct {
				ProviderOptions map[string]map[string]any `json:"provider_options"`
			} `json:"settings"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return out, fmt.Errorf("config: parse user layer: %w", err)
	}
	for tool, key := range toolResourceKeys {
		options := doc.Resources[key].Settings.ProviderOptions
		if len(options) == 0 {
			continue
		}
		if tool == ToolVideo {
			out.Video = options
		} else {
			out.Image = options
		}
	}
	return out, nil
}

// SaveToolOptions validates the options against the configured
// instances and writes them into the user layer, replacing the previous
// blocks (so a cleared knob disappears instead of lingering).
func SaveToolOptions(
	configDir string, opts ToolOptions, instances []Instance,
) error {
	if err := ValidateToolOptions(opts, instances); err != nil {
		return err
	}
	fresh, err := toolOptionsYAML(opts)
	if err != nil {
		return err
	}
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	path := filepath.Join(configDir, "opencraft.yaml")
	merged, err := mergeUserLayer(
		path,
		fresh,
		map[string]bool{
			toolResourceKeys[ToolImage]: true,
			toolResourceKeys[ToolVideo]: true,
		},
		map[string]bool{},
		map[string]bool{
			toolResourceKeys[ToolImage]: true,
			toolResourceKeys[ToolVideo]: true,
		},
		false,
	)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, merged, 0o600)
}

// toolOptionsYAML renders the user-layer blocks for the given options,
// omitting a tool entirely when it has nothing configured.
func toolOptionsYAML(opts ToolOptions) ([]byte, error) {
	doc := map[string]any{"resources": toolOptionResources(opts)}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("config: render tool options: %w", err)
	}
	return data, nil
}

// toolOptionResources renders the user-layer resource blocks for the
// given options, omitting a tool with nothing configured.
func toolOptionResources(opts ToolOptions) map[string]any {
	resources := map[string]any{}
	for tool, key := range toolResourceKeys {
		options := opts.optionsFor(tool)
		if len(options) == 0 {
			continue
		}
		resources[key] = map[string]any{
			"settings": map[string]any{"provider_options": options},
		}
	}
	return resources
}

// withToolOptions folds the given knob blocks into an already rendered
// user document, replacing both tool resources: a tool with nothing
// configured is dropped from the document, which — together with the
// caller replacing those keys — is what removes an emptied block from
// the layer instead of leaving it behind.
func withToolOptions(fresh []byte, opts ToolOptions) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(fresh, &doc); err != nil {
		return nil, fmt.Errorf("config: parse generated user layer: %w", err)
	}
	resources, _ := doc["resources"].(map[string]any)
	if resources == nil {
		resources = map[string]any{}
		doc["resources"] = resources
	}
	blocks := toolOptionResources(opts)
	for _, key := range toolResourceKeys {
		block, ok := blocks[key]
		if !ok {
			delete(resources, key)
			continue
		}
		resources[key] = block
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("config: render tool options: %w", err)
	}
	return data, nil
}

// ValidateToolOptions checks the options against the instances the user
// configured: every deployment id must exist, its driver must own the
// field vocabulary, and every value must fit the field.
func ValidateToolOptions(opts ToolOptions, instances []Instance) error {
	byID := make(map[string]Instance, len(instances))
	for i, in := range instances {
		byID[in.DeploymentID(i+1)] = in
	}
	for _, tool := range []string{ToolImage, ToolVideo} {
		options := opts.optionsFor(tool)
		ids := make([]string, 0, len(options))
		for id := range options {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			in, ok := byID[id]
			if !ok {
				return fmt.Errorf(
					"config: tool options: unknown provider %q", id)
			}
			prov, ok := ProviderFor(in)
			if !ok {
				return fmt.Errorf(
					"config: tool options: provider %q has no driver", id)
			}
			schema := ToolOptionSchema(tool, prov.Impl)
			if len(schema) == 0 {
				return fmt.Errorf(
					"config: tool options: %s does not support %s-specific options",
					prov.NameOrDefault(id), tool)
			}
			if err := validateToolOptionValues(schema, options[id]); err != nil {
				return fmt.Errorf(
					"config: tool options: provider %s: %w", id, err)
			}
		}
	}
	return nil
}

// NameOrDefault names a provider for error messages.
func (p Provider) NameOrDefault(id string) string {
	if strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	return id
}

// validateToolOptionValues checks one provider's option object against
// the field vocabulary: unknown paths fail, known ones must match their
// type, enum, and bounds.
func validateToolOptionValues(
	schema []ToolOptionField, values map[string]any,
) error {
	known := make(map[string]ToolOptionField, len(schema))
	for _, field := range schema {
		known[field.Name] = field
	}
	for _, field := range schema {
		leaf, ok := lookupOptionValue(values, field.Name)
		if !ok {
			continue
		}
		if err := validateToolOptionValue(field, leaf); err != nil {
			return fmt.Errorf("%s: %w", field.Name, err)
		}
	}
	return rejectUnknownOptionPaths(schema, values, "")
}

// rejectUnknownOptionPaths walks the option object and fails on any leaf
// the vocabulary does not declare, so a stale or misspelled key never
// reaches the driver.
func rejectUnknownOptionPaths(
	schema []ToolOptionField, values map[string]any, prefix string,
) error {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if nested, ok := values[name].(map[string]any); ok {
			if err := rejectUnknownOptionPaths(schema, nested, path); err != nil {
				return err
			}
			continue
		}
		if !optionPathDeclared(schema, path) {
			return fmt.Errorf("unknown option %q", path)
		}
		if optionHasDescendants(schema, path) {
			return fmt.Errorf("option %q must be an object", path)
		}
	}
	return nil
}

// optionPathDeclared reports whether the vocabulary declares a path,
// either exactly or as the ancestor of a declared nested field.
func optionPathDeclared(schema []ToolOptionField, path string) bool {
	for _, field := range schema {
		if field.Name == path || strings.HasPrefix(field.Name, path+".") {
			return true
		}
	}
	return false
}

// optionHasDescendants reports whether the vocabulary declares nested
// fields below a path, which makes a leaf at that path a type error.
func optionHasDescendants(schema []ToolOptionField, path string) bool {
	for _, field := range schema {
		if strings.HasPrefix(field.Name, path+".") {
			return true
		}
	}
	return false
}

// lookupOptionValue resolves a dotted path inside a nested option object.
func lookupOptionValue(values map[string]any, path string) (any, bool) {
	segments := strings.Split(path, ".")
	current := any(values)
	for _, segment := range segments {
		nested, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = nested[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// validateToolOptionValue checks one leaf against its field.
func validateToolOptionValue(field ToolOptionField, value any) error {
	switch field.Kind {
	case ToolOptionBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be a boolean")
		}
	case ToolOptionString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("must be a string")
		}
	case ToolOptionEnum:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}
		for _, allowed := range field.Values {
			if text == allowed {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s, got %q",
			strings.Join(field.Values, ", "), text)
	case ToolOptionInt, ToolOptionFloat:
		number, ok := optionNumber(value)
		if !ok {
			return fmt.Errorf("must be a number")
		}
		if field.Kind == ToolOptionInt && number != math.Trunc(number) {
			return fmt.Errorf("must be a whole number")
		}
		if err := checkBounds(field, number); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported field kind %q", field.Kind)
	}
	return nil
}

// optionNumber reads a JSON-decoded number (float64) or an int.
func optionNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// checkBounds applies a field's range.
func checkBounds(field ToolOptionField, value float64) error {
	if field.Min != nil {
		if field.ExclusiveMin && value <= *field.Min {
			return fmt.Errorf("must be greater than %g", *field.Min)
		}
		if !field.ExclusiveMin && value < *field.Min {
			return fmt.Errorf("must be at least %g", *field.Min)
		}
	}
	if field.Max != nil {
		if field.ExclusiveMax && value >= *field.Max {
			return fmt.Errorf("must be less than %g", *field.Max)
		}
		if !field.ExclusiveMax && value > *field.Max {
			return fmt.Errorf("must be at most %g", *field.Max)
		}
	}
	return nil
}

// FlattenToolOptions renders one provider's nested option object as the
// flat dotted map the settings page edits.
func FlattenToolOptions(
	schema []ToolOptionField, values map[string]any,
) map[string]any {
	out := make(map[string]any, len(schema))
	for _, field := range schema {
		if leaf, ok := lookupOptionValue(values, field.Name); ok {
			out[field.Name] = leaf
		}
	}
	return out
}

// NestToolOptions turns the flat dotted map the settings page submits
// back into the nested object the drivers decode, rejecting paths the
// vocabulary does not declare.
func NestToolOptions(
	schema []ToolOptionField, flat map[string]any,
) (map[string]any, error) {
	out := map[string]any{}
	names := make([]string, 0, len(flat))
	for name := range flat {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var field *ToolOptionField
		for i := range schema {
			if schema[i].Name == name {
				field = &schema[i]
				break
			}
		}
		if field == nil {
			return nil, fmt.Errorf("config: unknown option %q", name)
		}
		if err := validateToolOptionValue(*field, flat[name]); err != nil {
			return nil, fmt.Errorf("config: option %s: %w", name, err)
		}
		segments := strings.Split(name, ".")
		current := out
		for _, segment := range segments[:len(segments)-1] {
			next, ok := current[segment].(map[string]any)
			if !ok {
				next = map[string]any{}
				current[segment] = next
			}
			current = next
		}
		current[segments[len(segments)-1]] = flat[name]
	}
	return out, nil
}

// ServesImage reports whether the model declares image output.
func (m Model) ServesImage() bool {
	return len(m.Capabilities.Outputs) > 0 &&
		slices.Contains(m.Capabilities.Outputs, message.PartImage)
}

// ServesVideo reports whether the model declares video output.
func (m Model) ServesVideo() bool {
	return len(m.Capabilities.Outputs) > 0 &&
		slices.Contains(m.Capabilities.Outputs, message.PartVideo)
}
