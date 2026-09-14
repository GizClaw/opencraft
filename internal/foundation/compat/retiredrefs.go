package compat

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	yamlv4 "go.yaml.in/yaml/v4"
)

// This file owns the retired assembly variables: references an older
// build wrote into the user configuration layer as ${env:OPEN_CRAFT_*}
// and that the resolver-based assembly replaced with ${ocraft:*}
// (see orchestration/engine/assemble.go's resolver, which is the
// table's other half). A layer that still names one of them fails to
// expand while the runtime builds, so the shape is both reported to
// the user and repairable from Settings -> Diagnostics.
//
// The file keeps no I/O: reading, writing and locking the user layer
// belong to foundation/config.

// retiredRefs maps each retired assembly variable to the ${ocraft:*}
// reference that replaced it.
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
// resolver trims the reference path, so the spaced spelling expands
// (and fails) exactly like the canonical one and is reported too.
var retiredRefPattern = regexp.MustCompile(
	`\$\{\s*env\s*:\s*(OPEN_CRAFT_[A-Z0-9_]+)\s*\}`,
)

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

// RetiredEnvNames returns every retired assembly variable name the
// scan recognises, in table order. Callers use it to scrub the names a
// test or a shell may still export, so a layer cannot appear to
// resolve when it does not.
func RetiredEnvNames() []string {
	names := make([]string, 0, len(retiredRefs))
	for _, ref := range retiredRefs {
		names = append(names, ref.Env)
	}
	return names
}

// ScanRetiredRefs reports every live reference in one layer document,
// in document order. Comment-only mentions are skipped because
// nothing expands inside a YAML comment.
func ScanRetiredRefs(data []byte) []RetiredRef {
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

// retiredRefReplacement reports the replacement for one retired name
// and whether the reference is actually broken. A name that is still
// set in the process environment keeps resolving, so such a layer is
// the user's business and stays as it is.
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

// DropRetiredRefs removes every declaration below root that carries a
// broken retired reference, and reports the YAML paths it dropped.
//
// A sequence entry (a hook, a target, a profile) is removed whole:
// dropping only the broken field would leave a half-configured entry
// behind. Inside a mapping only the entry holding the reference is
// removed, so sibling settings survive, and containers left empty are
// pruned so they cannot shadow the built-in value with nothing.
func DropRetiredRefs(root *yamlv4.Node) []string {
	var removed []string
	dropRetiredRefs(root, "", &removed)
	return removed
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

// retiredScalarEnv reports the retired variable a scalar node names,
// when that reference can no longer resolve.
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

// subtreeRetiredEnv reports the first broken retired reference
// anywhere below node.
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
