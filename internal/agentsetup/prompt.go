package agentsetup

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/padmk/portside/internal/app"
)

//go:embed prompt.md
var promptSource string

var promptTmpl = template.Must(template.New("prompt").Parse(promptSource))

// Prompt is the request a user pastes into a coding agent so it sets up the
// agents, adds the user's projects and checks they start. Commands name this
// binary by its name, with its path when PATH does not have it.
func Prompt() string {
	path := ""
	if _, self := executable(&tools{}); self != app.ID {
		path = self
	}
	var b strings.Builder
	_ = promptTmpl.Execute(&b, map[string]string{"Name": app.Name, "ID": app.ID, "Path": path})
	return b.String()
}
