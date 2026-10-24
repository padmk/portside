package agentsetup

import (
	_ "embed"
	"runtime/debug"
	"strings"
	"text/template"

	"github.com/padmk/portside/internal/app"
)

var (
	//go:embed prompt.md
	setupSource string
	//go:embed share-prompt.md
	shareSource string

	setupTmpl = template.Must(template.New("setup").Parse(setupSource))
	shareTmpl = template.Must(template.New("share").Parse(shareSource))
)

// Prompt is the request a user pastes into a coding agent so it sets up the
// agents, adds the user's projects and checks they start.
func Prompt() string { return render(setupTmpl) }

// SharePrompt is the request a user pastes into a coding agent so it sets up
// two computers for each other: Tailscale on both, sharing on the one that
// runs the projects, and pairing.
func SharePrompt() string { return render(shareTmpl) }

// render fills a prompt. Commands name this binary by its name, with its path
// when PATH does not have it.
func render(t *template.Template) string {
	path := ""
	if _, self := executable(&tools{}); self != app.ID {
		path = self
	}
	var b strings.Builder
	_ = t.Execute(&b, map[string]string{"Name": app.Name, "ID": app.ID, "Path": path, "Cask": cask()})
	return b.String()
}

// cask is the Homebrew cask of the macOS app, `<owner>/tap/<id>`, from the
// GitHub module path the release is built from; "" for another module path.
func cask() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	parts := strings.Split(info.Main.Path, "/")
	if len(parts) < 2 || parts[0] != "github.com" {
		return ""
	}
	return parts[1] + "/tap/" + app.ID
}
