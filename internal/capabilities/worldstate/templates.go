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
	planTmpl        = template.Must(template.ParseFS(templateFS, "templates/plan.gotmpl"))
	gitTmpl         = template.Must(template.ParseFS(templateFS, "templates/git.gotmpl"))
	skillActivTmpl  = template.Must(template.ParseFS(templateFS, "templates/skill_activation.gotmpl"))
)

type permissionsData struct {
	Profile          string
	ApprovedPrefixes string
	YOLO             bool
	ReadOnly         bool
}

type environmentData struct {
	WorkspaceRoot     string
	CollaborationMode string
}

// planItemData is one checklist row for plan.gotmpl. Marker is the
// rendered checkbox ("[x]", "[~]", or "[ ]").
type planItemData struct {
	Step   string
	Marker string
	Status string
}

// planData is the view for plan.gotmpl.
type planData struct {
	Items       []planItemData
	Explanation string
}

// gitData is the view for git.gotmpl. Each text field arrives
// pre-bounded by the collector.
type gitData struct {
	Branch   string
	Status   string
	DiffStat string
	Diff     string
	DiffHint string
}

// skillActivationData is the view for skill_activation.gotmpl.
// Untrusted marks user-installed/third-party skills for a caution
// note; Body carries the SKILL.md body, a failure message, or an
// extra note plus the body.
type skillActivationData struct {
	Name      string
	Path      string
	Untrusted bool
	Staged    string
	Note      string
	Body      string
}

func render(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
