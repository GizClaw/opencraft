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

// Probe renders the typed extensions to attach for the requested
// provider knobs. extensionID is the provider-carried extension the
// drivers register (image_options, video_options, ...); a field is
// attached only to a provider whose decoder for that extension accepts
// it, and an empty result fails loudly instead of dropping the knob.
func Probe(
	tool string,
	assembly *inference.Assembly,
	extensionID string,
	fields map[string]any,
) (inference.Extensions, error) {
	if assembly == nil || len(fields) == 0 {
		return nil, nil
	}
	decoders := assembly.ExtensionDecoders()
	definitions := assembly.Providers()
	providers := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		providers = append(providers, definition.ID)
	}
	entries := Entries(decoders, providers, extensionID, fields)
	if len(entries) == 0 {
		return nil, errdefs.Validationf(
			"%s: no configured provider supports %s",
			tool, strings.Join(FieldNames(fields), " and "))
	}
	return inference.DecodeExtensions(
		entries, decoders, tool+" extensions")
}

// Entries keeps, per provider, the requested fields that provider's
// decoder for extensionID accepts. Each field is probed on its own so a
// provider that models one knob still receives it when it rejects
// another; the decoder is the authority on what a driver accepts, which
// keeps callers free of driver field tables.
func Entries(
	decoders map[string]inference.ExtensionDecoder,
	providers []string,
	extensionID string,
	fields map[string]any,
) []inference.ExtensionEntry {
	names := FieldNames(fields)
	var entries []inference.ExtensionEntry
	for _, provider := range providers {
		decoder, ok := decoders[provider+"/"+extensionID]
		if !ok {
			continue
		}
		accepted := make(map[string]any, len(names))
		for _, name := range names {
			probe, err := json.Marshal(map[string]any{name: fields[name]})
			if err != nil {
				continue
			}
			if _, err := decoder(probe); err != nil {
				continue
			}
			accepted[name] = fields[name]
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
	return entries
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
