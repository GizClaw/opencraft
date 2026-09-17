package inferenceext

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
)

// strictDecoder accepts only the listed top-level fields.
func strictDecoder(want ...string) inference.ExtensionDecoder {
	return func(fields json.RawMessage) (inference.Extension, error) {
		var bag map[string]json.RawMessage
		if err := json.Unmarshal(fields, &bag); err != nil {
			return nil, err
		}
		for name := range bag {
			known := false
			for _, allowed := range want {
				if name == allowed {
					known = true
				}
			}
			if !known {
				return nil, errors.New("unknown field " + name)
			}
		}
		return fakeExtension{}, nil
	}
}

// TestBuildProbesPerField pins the provider probe: a call knob is
// offered only to a provider whose decoder accepts it, a provider
// without the extension stays untouched, and a call knob no provider
// models fails the call.
func TestBuildProbesPerField(t *testing.T) {
	decoders := map[string]inference.ExtensionDecoder{
		"mask-vendor/image_options":    strictDecoder("mask"),
		"preview-vendor/image_options": strictDecoder("partial_images"),
		"other-vendor/music_options": func(json.RawMessage) (
			inference.Extension, error,
		) {
			return fakeExtension{}, nil
		},
	}
	providers := []string{"mask-vendor", "preview-vendor", "other-vendor"}
	entries, err := buildEntries(
		"generate_image",
		decoders,
		providers,
		"image_options",
		map[string]any{"mask": "mask-bytes", "partial_images": 2},
		nil,
	)
	if err != nil {
		t.Fatalf("buildEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want one per accepting provider", entries)
	}
	byProvider := map[string]map[string]json.RawMessage{}
	for _, entry := range entries {
		if entry.ID != "image_options" {
			t.Errorf("entry id = %q", entry.ID)
		}
		var bag map[string]json.RawMessage
		if err := json.Unmarshal(entry.Fields, &bag); err != nil {
			t.Fatal(err)
		}
		byProvider[entry.Provider] = bag
	}
	if len(byProvider["mask-vendor"]) != 1 {
		t.Errorf("mask vendor fields = %+v, want mask only",
			byProvider["mask-vendor"])
	}
	if len(byProvider["preview-vendor"]) != 1 {
		t.Errorf("preview vendor fields = %+v, want partial_images only",
			byProvider["preview-vendor"])
	}
	if _, ok := byProvider["other-vendor"]; ok {
		t.Error("a provider without the extension must stay untouched")
	}

	_, err = buildEntries(
		"generate_image", decoders, providers, "image_options",
		map[string]any{"nope": true}, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "no configured provider supports nope") {
		t.Fatalf("unsupported call knob error = %v", err)
	}
}

// TestBuildAttachesConfiguredProviderOptions pins the second source:
// configured options attach only to the provider they belong to, merge
// with the call knobs, and a configured option the owning provider
// rejects fails instead of being dropped.
func TestBuildAttachesConfiguredProviderOptions(t *testing.T) {
	decoders := map[string]inference.ExtensionDecoder{
		"openai-inst/image_options":    strictDecoder("mask", "background"),
		"bytedance-inst/image_options": strictDecoder("size_token"),
	}
	providers := []string{"openai-inst", "bytedance-inst"}
	entries, err := buildEntries(
		"generate_image", decoders, providers, "image_options",
		map[string]any{"mask": "mask-bytes"},
		map[string]map[string]any{
			"openai-inst":    {"background": "transparent"},
			"bytedance-inst": {"size_token": "2k"},
		},
	)
	if err != nil {
		t.Fatalf("buildEntries: %v", err)
	}
	got := map[string]map[string]json.RawMessage{}
	for _, entry := range entries {
		var bag map[string]json.RawMessage
		if err := json.Unmarshal(entry.Fields, &bag); err != nil {
			t.Fatal(err)
		}
		got[entry.Provider] = bag
	}
	if len(got["openai-inst"]) != 2 {
		t.Errorf("openai entry = %+v, want mask + background", got["openai-inst"])
	}
	if len(got["bytedance-inst"]) != 1 {
		t.Errorf("bytedance entry = %+v, want size_token only (no mask)",
			got["bytedance-inst"])
	}

	_, err = buildEntries(
		"generate_image", decoders, providers, "image_options", nil,
		map[string]map[string]any{"openai-inst": {"size_token": "2k"}},
	)
	if err == nil || !strings.Contains(err.Error(), "does not accept the configured option") {
		t.Fatalf("configured rejection error = %v", err)
	}

	_, err = buildEntries(
		"generate_image", decoders, providers, "image_options", nil,
		map[string]map[string]any{"gone-inst": {"background": "auto"}},
	)
	if err == nil || !strings.Contains(err.Error(), "has no image_options extension") {
		t.Fatalf("unknown configured provider error = %v", err)
	}
}

// TestDroppedRendersDecisions pins the reporting of fields a driver
// discarded, which is how a tool result says what the provider ignored.
func TestDroppedRendersDecisions(t *testing.T) {
	resp := inference.GenerateResponse{
		Metadata: inference.Metadata{
			Decisions: []inference.Decision{
				{
					Field:       "generate.intent.image.quality",
					Disposition: inference.Dropped,
					Reason:      "seedream has no quality parameter",
				},
				{
					Field:       "generate.intent.image.size",
					Disposition: inference.Native,
				},
			},
		},
	}
	dropped := Dropped(resp)
	if len(dropped) != 1 ||
		!strings.Contains(dropped[0], "seedream has no quality parameter") {
		t.Fatalf("dropped = %v", dropped)
	}
}

// TestExplainAppendsRejectionReason pins the cause-chain surfacing:
// inference errors render only kind and field, so the readable reason
// has to be appended for the caller.
func TestExplainAppendsRejectionReason(t *testing.T) {
	err := inference.NewError(
		inference.InvalidExtension,
		model.OperationGenerate,
		inference.FieldID("extension.vendor-inst.image_options.mask"),
		errdefs.Validation(errors.New(
			`vendor-inst: image masks require azure routing`)),
	)
	explained := Explain(err)
	for _, want := range []string{
		"invalid_extension",
		"image masks require azure routing",
	} {
		if !strings.Contains(explained.Error(), want) {
			t.Errorf("explained error missing %q: %v", want, explained)
		}
	}

	// Provider failures keep their own message: the cause carries the
	// provider payload, which must not enter a tool result.
	providerErr := inference.NewError(
		inference.ProviderFailure,
		model.OperationGenerate,
		"",
		errdefs.Validation(errors.New("provider echoed the prompt")),
	)
	if got := Explain(providerErr); got.Error() != providerErr.Error() {
		t.Errorf("provider failure was rewritten: %v", got)
	}
}

// fakeExtension stands in for a typed provider extension.
type fakeExtension struct{}

func (fakeExtension) ProviderID() string  { return "probe" }
func (fakeExtension) ExtensionID() string { return "image_options" }
func (fakeExtension) ActiveFields() []inference.ExtensionField {
	return []inference.ExtensionField{"probe"}
}
func (fakeExtension) Validate() error              { return nil }
func (e fakeExtension) Clone() inference.Extension { return e }
