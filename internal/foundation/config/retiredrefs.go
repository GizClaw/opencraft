package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yamlv4 "go.yaml.in/yaml/v4"
)

// retiredRefs maps the OPEN_CRAFT_* assembly variables retired by the
// resolver-based engine assembly (#99) to the ${ocraft:*} references that
// replaced them. The table mirrors ocraftResolver in
// orchestration/engine/assemble.go: assembly path values now travel with
// the flowcraft builder instead of the process environment, so a
// persisted document that still names the variable fails to expand with
// `env "OPEN_CRAFT_WORKDIR" is not set` while the runtime builds.
var retiredRefs = []struct {
	Env string
	Ref string
}{
	{Env: "OPEN_CRAFT_WORKDIR", Ref: "${ocraft:WORKDIR}"},
	{Env: "OPEN_CRAFT_CACHE", Ref: "${ocraft:CACHE}"},
	{Env: "OPEN_CRAFT_DATA_DIR", Ref: "${ocraft:DATA_DIR}"},
	{Env: "OPEN_CRAFT_WORKSPACE_DIR", Ref: "${ocraft:WORKSPACE_DIR}"},
	{Env: "OPEN_CRAFT_SESSIONS_DIR", Ref: "${ocraft:SESSIONS_DIR}"},
	{Env: "OPEN_CRAFT_APPROVALS", Ref: "${ocraft:APPROVALS}"},
	{Env: "OPEN_CRAFT_TOOL_CACHE", Ref: "${ocraft:TOOL_CACHE}"},
	{Env: "OPEN_CRAFT_AUDIT_DIR", Ref: "${ocraft:AUDIT_DIR}"},
}

// retiredRefPattern matches one ${env:OPEN_CRAFT_*} reference. The
// resolver trims the reference path, so the spaced spelling expands (and
// fails) exactly like the canonical one and is reported too.
var retiredRefPattern = regexp.MustCompile(
	`\$\{\s*env\s*:\s*(OPEN_CRAFT_[A-Z0-9_]+)\s*\}`,
)

// UserLayerFile returns the user configuration layer path inside
// configDir.
func UserLayerFile(configDir string) string {
	return filepath.Join(configDir, "opencraft.yaml")
}

// RetiredRef is one live user-layer reference to a retired assembly
// variable.
type RetiredRef struct {
	// Env is the retired variable name, e.g. OPEN_CRAFT_WORKDIR.
	Env string
	// Ref is the ${ocraft:...} spelling that replaced it.
	Ref string
	// Line is the 1-based line the reference sits on.
	Line int
}

// retiredRefReplacement reports the replacement for one retired name and
// whether the reference is actually broken. A name that is still set in
// the process environment keeps resolving, so such a layer is the user's
// business and stays as it is.
func retiredRefReplacement(env string) (string, bool) {
	for _, ref := range retiredRefs {
		if ref.Env != env {
			continue
		}
		if _, set := os.LookupEnv(env); set {
			return "", false
		}
		return ref.Ref, true
	}
	return "", false
}

// FindRetiredRefs reports every live reference in the user layer to a
// retired assembly variable, in document order. A missing layer has no
// references; comment-only mentions are skipped because nothing expands
// inside a YAML comment.
func FindRetiredRefs(configDir string) ([]RetiredRef, error) {
	data, err := os.ReadFile(UserLayerFile(configDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: read user layer: %w", err)
	}
	return scanRetiredRefs(data), nil
}

// scanRetiredRefs returns the live references in one document.
func scanRetiredRefs(data []byte) []RetiredRef {
	var out []RetiredRef
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, match := range retiredRefPattern.FindAllStringSubmatch(line, -1) {
			ref, broken := retiredRefReplacement(match[1])
			if !broken {
				continue
			}
			out = append(out, RetiredRef{
				Env: match[1], Ref: ref, Line: i + 1,
			})
		}
	}
	return out
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
// A sequence entry (a hook, a target, a profile) is removed whole:
// dropping only the broken field would leave a half-configured entry
// behind. Inside a mapping only the entry holding the reference is
// removed, so sibling settings survive, and containers left empty are
// pruned so they cannot shadow the built-in value with nothing. The
// pre-repair document is kept next to the layer, and the caller gets the
// list of removed paths to report.
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
	if len(scanRetiredRefs(data)) == 0 {
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
	var removed []string
	dropRetiredRefs(root, "", &removed)
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

// dropRetiredRefs removes the broken declarations below node, appends
// their YAML paths to removed, and reports whether node ended up empty.
func dropRetiredRefs(node *yamlv4.Node, path string, removed *[]string) bool {
	switch node.Kind {
	case yamlv4.MappingNode:
		kept := make([]*yamlv4.Node, 0, len(node.Content))
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			childPath := joinNodePath(path, key.Value)
			if _, broken := retiredScalarEnv(value); broken {
				*removed = append(*removed, childPath)
				continue
			}
			if dropRetiredRefs(value, childPath, removed) {
				continue
			}
			kept = append(kept, key, value)
		}
		node.Content = kept
		return len(kept) == 0
	case yamlv4.SequenceNode:
		kept := make([]*yamlv4.Node, 0, len(node.Content))
		for i, item := range node.Content {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if _, broken := subtreeRetiredEnv(item); broken {
				*removed = append(*removed, childPath)
				continue
			}
			kept = append(kept, item)
		}
		node.Content = kept
		return len(kept) == 0
	}
	return false
}

// retiredScalarEnv reports the retired variable a scalar node names, when
// that reference can no longer resolve.
func retiredScalarEnv(node *yamlv4.Node) (string, bool) {
	if node == nil || node.Kind != yamlv4.ScalarNode {
		return "", false
	}
	match := retiredRefPattern.FindStringSubmatch(node.Value)
	if match == nil {
		return "", false
	}
	if _, broken := retiredRefReplacement(match[1]); !broken {
		return "", false
	}
	return match[1], true
}

// subtreeRetiredEnv reports the first broken retired reference anywhere
// below node.
func subtreeRetiredEnv(node *yamlv4.Node) (string, bool) {
	if env, broken := retiredScalarEnv(node); broken {
		return env, true
	}
	for _, child := range node.Content {
		if env, broken := subtreeRetiredEnv(child); broken {
			return env, true
		}
	}
	return "", false
}

// joinNodePath appends one mapping key to a YAML path.
func joinNodePath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
