package worldstate

import (
	"bytes"
	"embed"
	"text/template"
)

//go:embed templates/*.gotmpl
var templateFS embed.FS

var (
	permissionsTmpl = template.Must(template.ParseFS(templateFS, "templates/permissions.gotmpl"))
	environmentTmpl = template.Must(template.ParseFS(templateFS, "templates/environment.gotmpl"))
)

type permissionsData struct {
	Profile          string
	ApprovedPrefixes string
	YOLO             bool
}

type environmentData struct {
	WorkspaceRoot     string
	CollaborationMode string
}

func render(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
