package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	yamlv4 "go.yaml.in/yaml/v4"
	"sigs.k8s.io/yaml"
)

// Merging the user configuration layer (~/.opencraft/config/opencraft.yaml).
// Generated resources are deep-merged into the existing document so the
// sections a caller does not manage survive the write.

// mergeUserLayer merges a freshly generated user document over the
// existing user layer, preserving top-level sections and resources the
// generator does not own. replaceKeys are resources taken verbatim
// from the fresh document; mergeKeys are resources deep-merged (the
// fresh document contributes only the keys it sets); dropKeys are
// resources removed from the user layer entirely, which is how a
// cleared section disappears instead of lingering. Comments are
// preserved through yaml.Node.
func mergeUserLayer(
	path string,
	fresh []byte,
	replaceKeys map[string]bool,
	mergeKeys map[string]bool,
	dropKeys map[string]bool,
	dropProviderKeys bool,
) ([]byte, error) {
	oldData, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fresh, nil
		}
		return nil, fmt.Errorf("config: read user layer: %w", err)
	}
	// Strict parse first: the comment-preserving Node parser is too
	// lenient to detect a broken user layer, and overwriting one would
	// silently destroy the user's hand-written config.
	var probe map[string]any
	if err := yaml.Unmarshal(oldData, &probe); err != nil {
		return nil, fmt.Errorf("config: parse existing user layer: %w", err)
	}
	var oldNode, newRoot yamlv4.Node
	if err := yamlv4.Unmarshal(oldData, &oldNode); err != nil {
		return nil, fmt.Errorf("config: parse existing user layer: %w", err)
	}
	if err := yamlv4.Unmarshal(fresh, &newRoot); err != nil {
		return nil, fmt.Errorf("config: parse generated user layer: %w", err)
	}
	if len(oldNode.Content) == 0 {
		// An empty user layer (e.g. a file created with `touch`) has no
		// data to preserve: treat it like a missing layer and write the
		// fresh document, so first-time configuration cannot be blocked
		// by an empty file.
		return fresh, nil
	}
	if oldNode.Content[0].Kind != yamlv4.MappingNode {
		return nil, fmt.Errorf(
			"config: existing user layer is not a YAML mapping (kind %d, %d entries); refusing to overwrite it",
			func() uint32 {
				if len(oldNode.Content) > 0 {
					return uint32(oldNode.Content[0].Kind)
				}
				return uint32(0)
			}(),
			len(oldNode.Content),
		)
	}
	oldDoc := oldNode.Content[0]
	newDoc := newRoot.Content[0]

	// Preserve top-level sections the generator does not write (e.g. a
	// custom agents section).
	for i := 0; i+1 < len(oldDoc.Content); i += 2 {
		key := oldDoc.Content[i].Value
		if key == "resources" {
			continue
		}
		if findMappingKey(newDoc, key) == nil {
			newDoc.Content = append(newDoc.Content, oldDoc.Content[i], oldDoc.Content[i+1])
		}
	}

	// Merge resources: generator-owned keys are replaced (or
	// deep-merged), everything else is preserved.
	oldRes := findMappingKey(oldDoc, "resources")
	newRes := findMappingKey(newDoc, "resources")
	if oldRes != nil && newRes != nil && len(oldRes.Content) > 0 {
		for i := 0; i+1 < len(oldRes.Content); i += 2 {
			key := oldRes.Content[i].Value
			if replaceKeys[key] ||
				dropKeys[key] ||
				(dropProviderKeys && strings.HasPrefix(key, "provider.")) {
				continue
			}
			if mergeKeys[key] {
				oldResVal := oldRes.Content[i+1]
				if freshRes := findMappingKey(newRes, key); freshRes != nil &&
					freshRes.Kind == yamlv4.MappingNode &&
					oldResVal.Kind == yamlv4.MappingNode {
					// The generated keys win, the layer's other keys
					// survive: merge fresh into the existing resource
					// and hand the merged node to the output document.
					mergeMapping(oldResVal, freshRes)
					setMappingValue(newRes, key, oldResVal)
					continue
				}
			}
			if findMappingKey(newRes, key) == nil {
				newRes.Content = append(newRes.Content, oldRes.Content[i], oldRes.Content[i+1])
			}
		}
	}

	var buf bytes.Buffer
	enc := yamlv4.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&newRoot); err != nil {
		return nil, fmt.Errorf("config: encode merged user layer: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// findMappingKey returns the value node for key in a mapping node, or
// nil when absent.
func findMappingKey(mapping *yamlv4.Node, key string) *yamlv4.Node {
	if mapping == nil {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMappingValue points an existing key of a mapping node at a
// different value node; a key that is not there is left alone (merge
// callers only re-target keys the fresh document has).
func setMappingValue(mapping *yamlv4.Node, key string, value *yamlv4.Node) {
	if mapping == nil {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
}

// mergeMapping deep-merges src into dst in place: mapping pairs merge
// recursively, every other value replaces. The src nodes are appended
// as-is so their comments survive.
func mergeMapping(dst, src *yamlv4.Node) {
	if dst == nil || src == nil || dst.Kind != yamlv4.MappingNode || src.Kind != yamlv4.MappingNode {
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		srcKey := src.Content[i].Value
		srcVal := src.Content[i+1]
		if dstVal := findMappingKey(dst, srcKey); dstVal != nil {
			if dstVal.Kind == yamlv4.MappingNode && srcVal.Kind == yamlv4.MappingNode {
				mergeMapping(dstVal, srcVal)
				continue
			}
			// Replace the existing value pair in place.
			for j := 0; j+1 < len(dst.Content); j += 2 {
				if dst.Content[j].Value == srcKey {
					dst.Content[j+1] = srcVal
					break
				}
			}
			continue
		}
		dst.Content = append(dst.Content, src.Content[i], srcVal)
	}
}

// yamlQuote quotes a plain scalar safely (single-quote style).
func yamlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
