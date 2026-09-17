// Package inferenceext carries the provider-extension plumbing shared
// by the generation tools (generate_image, generate_video): probing
// which configured providers model a set of provider-specific knobs,
// verifying after the call that the executed provider actually applied
// them, and surfacing the readable reason a local rejection keeps in
// its cause chain.
package inferenceext

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
)

// Build renders the typed extensions to attach for one request from two
// sources. callKnobs are the per-call knobs the tool arguments named:
// they are offered to every provider whose decoder models them, and a
// knob no configured provider models fails the call. providerOptions are
// the knobs the user configured for one deployment each, keyed by
// provider id: they attach only to that provider, and a provider whose
// decoder rejects a configured option fails the call too — silently
// ignoring an explicitly configured knob is the behavior this exists to
// remove. extensionID is the provider-carried extension the drivers
// register (image_options, video_options, ...). Both sources merge per
// provider, so a provider never carries two entries for one extension.
func Build(
	tool string,
	assembly *inference.Assembly,
	extensionID string,
	callKnobs map[string]any,
	providerOptions map[string]map[string]any,
) (inference.Extensions, error) {
	if assembly == nil || (len(callKnobs) == 0 && len(providerOptions) == 0) {
		return nil, nil
	}
	decoders := assembly.ExtensionDecoders()
	definitions := assembly.Providers()
	providers := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		providers = append(providers, definition.ID)
	}
	entries, err := buildEntries(
		tool, decoders, providers, extensionID, callKnobs, providerOptions)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return inference.DecodeExtensions(
		entries, decoders, tool+" extensions")
}

// buildEntries merges both knob sources into one entry per provider and
// applies the loud failures: a call knob no provider models, a
// configured option the owning provider rejects, and a configured
// provider that registers no such extension.
func buildEntries(
	tool string,
	decoders map[string]inference.ExtensionDecoder,
	providers []string,
	extensionID string,
	callKnobs map[string]any,
	providerOptions map[string]map[string]any,
) ([]inference.ExtensionEntry, error) {
	callNames := FieldNames(callKnobs)
	callAccepted := make(map[string]bool, len(callNames))
	var entries []inference.ExtensionEntry
	for _, provider := range providers {
		decoder, ok := decoders[provider+"/"+extensionID]
		if !ok {
			continue
		}
		accepted := make(map[string]any)
		for _, name := range callNames {
			if !decoderAccepts(decoder, name, callKnobs[name]) {
				continue
			}
			accepted[name] = callKnobs[name]
			callAccepted[name] = true
		}
		configured := providerOptions[provider]
		for _, name := range FieldNames(configured) {
			if !decoderAccepts(decoder, name, configured[name]) {
				return nil, errdefs.Validationf(
					"%s: provider %s does not accept the configured option %s",
					tool, provider, name)
			}
			accepted[name] = configured[name]
		}
		if len(accepted) == 0 {
			continue
		}
		bag, err := json.Marshal(accepted)
		if err != nil {
			continue
		}
		entries = append(entries, inference.ExtensionEntry{
			Provider: provider,
			ID:       extensionID,
			Fields:   bag,
		})
	}
	var unsupported []string
	for _, name := range callNames {
		if !callAccepted[name] {
			unsupported = append(unsupported, name)
		}
	}
	if len(unsupported) > 0 {
		return nil, errdefs.Validationf(
			"%s: no configured provider supports %s",
			tool, strings.Join(unsupported, " and "))
	}
	for provider := range providerOptions {
		if _, ok := decoders[provider+"/"+extensionID]; ok {
			continue
		}
		return nil, errdefs.Validationf(
			"%s: provider %s has no %s extension to configure",
			tool, provider, extensionID)
	}
	return entries, nil
}

// decoderAccepts probes one provider decoder with a single field, which
// is how the tool learns what a driver models without carrying a driver
// field table of its own.
func decoderAccepts(
	decoder inference.ExtensionDecoder, name string, value any,
) bool {
	probe, err := json.Marshal(map[string]any{name: value})
	if err != nil {
		return false
	}
	_, err = decoder(probe)
	return err == nil
}

// FieldNames returns the field names in deterministic order.
func FieldNames(fields map[string]any) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Verify reports that the executed provider applied every
// provider-addressed knob the request attached. A route fallback can
// move the request to a provider that does not model a field — the
// extension is inert there — and the call would otherwise succeed with
// the constraint silently missing.
func Verify(
	tool string,
	resp inference.GenerateResponse,
	extensions inference.Extensions,
) error {
	requested := ActiveFieldNames(extensions)
	if len(requested) == 0 {
		return nil
	}
	executed := resp.Metadata.Model.Provider
	if executed == "" {
		return errdefs.Validationf(
			"%s: the response names no provider, so %s cannot be verified",
			tool, strings.Join(requested, " and "))
	}
	var extension inference.Extension
	for _, candidate := range extensions {
		if candidate.ProviderID() == executed {
			extension = candidate
			break
		}
	}
	if extension == nil {
		return errdefs.Validationf(
			"%s: %s does not support %s, so the request was routed to a "+
				"model that cannot apply it",
			tool, Label(resp.Metadata.Model),
			strings.Join(requested, " and "))
	}
	applied := make(map[inference.FieldID]bool, len(resp.Metadata.Decisions))
	for _, decision := range resp.Metadata.Decisions {
		if decision.Disposition == inference.Native {
			applied[decision.Field] = true
		}
	}
	var missing []string
	for _, field := range extension.ActiveFields() {
		if !applied[field.Qualify(extension)] {
			missing = append(missing, string(field))
		}
	}
	if len(missing) > 0 {
		return errdefs.Validationf(
			"%s: %s did not apply %s",
			tool, Label(resp.Metadata.Model),
			strings.Join(missing, " and "))
	}
	return nil
}

// ActiveFieldNames lists the unqualified provider knob names a request
// attached, so a verification failure can name them.
func ActiveFieldNames(extensions inference.Extensions) []string {
	seen := make(map[string]bool, len(extensions))
	var names []string
	for _, extension := range extensions {
		for _, field := range extension.ActiveFields() {
			name := string(field)
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Label renders a model id as "provider/name" (or just "name" when the
// provider is empty).
func Label(id model.ModelID) string {
	if id.Provider == "" {
		return id.Name
	}
	return id.Provider + "/" + id.Name
}

// Explain appends the reason a local rejection carries in its cause
// chain. Inference and route errors render only their kind and field
// (that string is what stays safe for routine logs), so the actionable
// part — why a driver refused a knob — would otherwise never reach the
// tool result. Provider failures keep their own message: their cause
// carries the provider payload, which belongs in the error chain, not
// in a tool result.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	reason, ok := rejectionReason(err)
	if !ok || reason == "" || strings.Contains(err.Error(), reason) {
		return err
	}
	return fmt.Errorf("%s: %s", err.Error(), reason)
}

// Dropped renders the compile decisions a driver discarded, so a tool
// result can tell the caller which request fields the provider ignored
// (a Seedream model dropping image quality, say) instead of letting them
// vanish silently. The report order is preserved.
func Dropped(resp inference.GenerateResponse) []string {
	var out []string
	for _, decision := range resp.Metadata.Decisions {
		if decision.Disposition != inference.Dropped {
			continue
		}
		text := string(decision.Field)
		if decision.Reason != "" {
			text += ": " + decision.Reason
		}
		out = append(out, text)
	}
	return out
}

// rejectionReason extracts the human reason of a local compile-time
// rejection, or ok=false for anything else (including provider
// failures).
func rejectionReason(err error) (string, bool) {
	var inferenceErr *inference.Error
	if !errors.As(err, &inferenceErr) {
		return "", false
	}
	switch inferenceErr.Kind {
	case inference.InvalidRequest,
		inference.UnsupportedFeature,
		inference.InvalidExtension,
		inference.UnsupportedOperation,
		inference.UnknownProvider,
		inference.UnknownModel,
		inference.UnknownProfile,
		inference.PolicyDenied:
	default:
		return "", false
	}
	return deepestCause(inferenceErr), true
}

// deepestCause returns the innermost message of an error's cause chain,
// which is where the drivers put the readable reason.
func deepestCause(err error) string {
	text := ""
	for depth := 0; depth < 8; depth++ {
		cause := errors.Unwrap(err)
		if cause == nil {
			break
		}
		text = cause.Error()
		err = cause
	}
	return text
}
