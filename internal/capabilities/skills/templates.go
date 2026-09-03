package skills

import (
	"bytes"
	"embed"
	"strings"
	"text/template"
)

//go:embed templates/*.gotmpl
var templateFS embed.FS

var skillsTmpl = template.Must(
	template.ParseFS(templateFS, "templates/skills.gotmpl"))

// skillRow is the per-skill view passed to the section template.
type skillRow struct {
	Name        string
	Description string
	Path        string
}

// renderSkillsSection renders the per-turn "## Skills" metadata list.
func renderSkillsSection(skills []SkillMetadata) string {
	rows := make([]skillRow, 0, len(skills))
	for _, sk := range skills {
		rows = append(rows, skillRow{
			Name:        sk.Name,
			Description: truncateUTF8(sk.Description, maxDescriptionLen),
			Path:        sk.Path,
		})
	}
	var buf bytes.Buffer
	if err := skillsTmpl.Execute(&buf, rows); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}
