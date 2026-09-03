package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// builtinFS holds the system skills shipped with opencraft (plan,
// skill-creator, skill-installer). They never touch disk: metadata is
// registered statically and ReadFull serves the body straight from the
// binary, so there is no writable "system" area on disk for the model
// to tamper with.
//
//go:embed assets/skills
var builtinFS embed.FS

const builtinPrefix = "builtin://"

// builtinSkills returns the parsed embedded system skills, ordered by
// directory name.
func builtinSkills() []SkillMetadata {
	entries, err := fs.ReadDir(builtinFS, "assets/skills")
	if err != nil {
		return nil
	}
	var out []SkillMetadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, err := fs.ReadFile(builtinFS,
			path.Join("assets/skills", name, "SKILL.md"))
		if err != nil {
			continue
		}
		res, err := parseBytes(builtinPrefix+name+"/SKILL.md", data)
		if err != nil {
			continue
		}
		sk := res.Metadata
		sk.Path = builtinPrefix + name + "/SKILL.md"
		sk.Scope = "builtin"
		sk.Depth = -1 // lowest duplicate-name priority
		out = append(out, sk)
	}
	return out
}

// readBuiltin serves the body of one embedded system skill.
func readBuiltin(skillPath string) (string, error) {
	rel := strings.TrimPrefix(skillPath, builtinPrefix)
	data, err := fs.ReadFile(builtinFS, path.Join("assets/skills", rel))
	if err != nil {
		return "", fmt.Errorf("read embedded skill: %w", err)
	}
	_, body, err := splitFrontmatter(data)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
