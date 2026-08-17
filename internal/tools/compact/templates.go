package compact

import (
	"bytes"
	"embed"
	"text/template"
)

//go:embed templates/*.gotmpl
var templateFS embed.FS

var condenseTmpl = template.Must(
	template.ParseFS(templateFS, "templates/system.gotmpl"))

// renderSystemPrompt renders the condensation instruction as the
// system message; the conversation transcript travels as the user
// input.
func renderSystemPrompt() (string, error) {
	var buf bytes.Buffer
	if err := condenseTmpl.Execute(&buf, nil); err != nil {
		return "", err
	}
	return buf.String(), nil
}
