package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	yamlv4 "go.yaml.in/yaml/v4"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// RetiredRef is one live user-layer reference to a retired assembly
// variable (see compat.ScanRetiredRefs).
type RetiredRef = compat.RetiredRef

// UserLayerFile returns the user configuration layer path inside
// configDir.
func UserLayerFile(configDir string) string {
	return filepath.Join(configDir, "opencraft.yaml")
}

// FindRetiredRefs reports every live reference in the user layer to a
// retired assembly variable, in document order. A missing layer has no
// references.
func FindRetiredRefs(configDir string) ([]RetiredRef, error) {
	data, err := os.ReadFile(UserLayerFile(configDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: read user layer: %w", err)
	}
	return compat.ScanRetiredRefs(data), nil
}

// retiredRefsError anchors the failure to the layer, the file and the
// offending references. Without it the user sees flowcraft's
// `env "OPEN_CRAFT_WORKDIR" is not set` raised from inside deployment,
// which names neither the file nor the replacement.
func retiredRefsError(configDir string, refs []RetiredRef) error {
	first := refs[0]
	message := fmt.Sprintf(
		"config: user layer %s:%d references ${env:%s}, which no longer "+
			"resolves: use %s. The OPEN_CRAFT_* assembly variables were "+
			"retired in favor of the ${ocraft:*} scheme",
		UserLayerFile(configDir), first.Line, first.Env, first.Ref)
	if extra := len(refs) - 1; extra > 0 {
		message += fmt.Sprintf(" (%d more obsolete reference(s) follow)", extra)
	}
	return errors.New(
		message + "; update the file by hand, or run the config " +
			"compatibility repair in Settings -> Diagnostics")
}

// RetiredRefsRepair is the outcome of RepairRetiredRefs. Nothing is
// written (and Backup is empty) when the layer had no broken reference.
type RetiredRefsRepair struct {
	// File is the user layer the repair inspected.
	File string
	// Backup is the pre-repair copy, empty when nothing was written.
	Backup string
	// Removed lists the YAML paths dropped from the layer, e.g.
	// "agents.assistant.prepare[0]".
	Removed []string
}

// RepairRetiredRefs removes the user-layer declarations that reference
// retired assembly variables and writes the layer back, so the built-in
// layer supplies the declaration again instead of the runtime failing to
// expand it.
//
// Which shapes count as a broken reference, and what is dropped with
// them, is compat.DropRetiredRefs. The pre-repair document is kept next
// to the layer, and the caller gets the list of removed paths to
// report.
func RepairRetiredRefs(configDir string) (RetiredRefsRepair, error) {
	path := UserLayerFile(configDir)
	result := RetiredRefsRepair{File: path}
	// Share the user-layer write lock with the settings writers.
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return result, fmt.Errorf("config: read user layer %s: %w", path, err)
	}
	if len(compat.ScanRetiredRefs(data)) == 0 {
		return result, nil
	}
	var doc yamlv4.Node
	if err := yamlv4.Unmarshal(data, &doc); err != nil {
		return result, fmt.Errorf("config: parse user layer %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yamlv4.MappingNode {
		return result, fmt.Errorf(
			"config: user layer %s is not a YAML mapping; repair it by hand",
			path)
	}
	root := doc.Content[0]
	removed := compat.DropRetiredRefs(root)
	if len(removed) == 0 {
		// Something matched the text but not a live document value (for
		// example a quoted comment); leave the file alone.
		return result, nil
	}
	if len(root.Content) == 0 {
		// An empty layer is still a valid document, but keep the
		// version tag the writers emit so the file stays recognizable.
		root.Content = []*yamlv4.Node{
			{Kind: yamlv4.ScalarNode, Value: "version"},
			{Kind: yamlv4.ScalarNode, Value: "v1"},
		}
	}
	var buf bytes.Buffer
	enc := yamlv4.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return result, fmt.Errorf("config: encode repaired user layer: %w", err)
	}
	if err := enc.Close(); err != nil {
		return result, fmt.Errorf("config: encode repaired user layer: %w", err)
	}
	backup := path + ".bak"
	if err := writeFileAtomic(backup, data, 0o600); err != nil {
		return result, fmt.Errorf("config: write user layer backup: %w", err)
	}
	if err := writeFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		return result, fmt.Errorf("config: write repaired user layer: %w", err)
	}
	result.Backup = backup
	result.Removed = removed
	return result, nil
}
