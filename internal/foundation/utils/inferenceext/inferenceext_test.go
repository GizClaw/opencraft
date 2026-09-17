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

// TestEntriesProbesPerField pins the provider probe: each field is
// offered only to a provider whose decoder accepts it, and a provider
// without the extension stays untouched.
func TestEntriesProbesPerField(t *testing.T) {
	strict := func(fields json.RawMessage, want ...string) (
		inference.Extension, error,
	) {
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
	decoders := map[string]inference.ExtensionDecoder{
		"mask-vendor/image_options": func(fields json.RawMessage) (
			inference.Extension, error,
		) {
			return strict(fields, "mask")
		},
		"preview-vendor/image_options": func(fields json.RawMessage) (
			inference.Extension, error,
		) {
			return strict(fields, "partial_images")
		},
		"other-vendor/music_options": func(json.RawMessage) (
			inference.Extension, error,
		) {
			return fakeExtension{}, nil
		},
	}
	entries := Entries(
		decoders,
		[]string{"mask-vendor", "preview-vendor", "other-vendor"},
		"image_options",
		map[string]any{"mask": "mask-bytes", "partial_images": 2},
	)
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
