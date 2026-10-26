package discover

import (
	"path/filepath"
	"slices"

	"github.com/padmk/portside/internal/config"
)

// CommandKind names the tool a server command in dir starts, following
// package scripts the way detection does, so the dashboard can show how to
// bind that tool to 127.0.0.1: "compose", "vite", "next", "astro", "nuxt",
// "nuxt2", "remix" and "react-router" (their Vite dev servers),
// "remix-serve" (remix-serve, react-router-serve and the classic remix dev,
// which read HOST), "node" (a script run by node, tsx, bun and similar, or
// Nest), "go", "rails", "phoenix", "uvicorn", "fastapi", "flask",
// "django", "gunicorn", "hypercorn" and "http.server". It returns "" for
// any other command.
func CommandKind(dir, cmd string) string {
	if config.IsComposeCommand(cmd) {
		return "compose"
	}
	pkg, _ := readPackageJSON(dir)
	run := resolveScript(pkg.Scripts, cmd)
	if !run.simple || run.delegates || len(run.words) == 0 {
		return ""
	}
	words := run.words
	if len(words) > 2 && words[0] == "bundle" && words[1] == "exec" {
		words = words[2:]
	}
	if k := otherKind(words); k != "" {
		return k
	}
	if k := pythonKind(pythonIdentity(words)); k != "" {
		return k
	}
	return nodeKind(words, pkg)
}

// otherKind recognizes Go, Rails and Phoenix servers.
func otherKind(words []string) string {
	prog, sub := words[0], ""
	if len(words) > 1 {
		sub = words[1]
	}
	switch {
	case prog == "go" && sub == "run", prog == "air":
		return "go"
	case prog == "rails" && (sub == "server" || sub == "s"):
		return "rails"
	case prog == "mix" && sub == "phx.server", prog == "iex" && slices.Contains(words, "phx.server"):
		return "phoenix"
	}
	return ""
}

// pythonKind recognizes Python servers by the words pythonIdentity leaves.
func pythonKind(words []string) string {
	if len(words) == 0 {
		return ""
	}
	prog, sub := words[0], ""
	if len(words) > 1 {
		sub = words[1]
	}
	switch {
	case prog == "uvicorn", prog == "hypercorn", prog == "gunicorn", prog == "http.server":
		return prog
	case prog == "fastapi" && (sub == "dev" || sub == "run"):
		return "fastapi"
	case prog == "flask" && slices.Contains(words, "run"):
		return "flask"
	case prog == "python" && filepath.Base(sub) == "manage.py" && slices.Contains(words, "runserver"),
		prog == "django-admin" && sub == "runserver":
		return "django"
	}
	return ""
}

// nodeKind recognizes the JavaScript dev servers detection knows.
func nodeKind(words []string, pkg pkgJSON) string {
	prog, sub := words[0], ""
	if len(words) > 1 {
		sub = words[1]
	}
	switch {
	case prog == "remix-serve", prog == "react-router-serve", prog == "remix" && sub == "dev":
		return "remix-serve"
	case prog == "nuxt" && (sub == "" || sub[0] == '-' || sub == "dev" && nuxt2(pkg)):
		return "nuxt2"
	}
	cli := nodeServer(words, pkg)
	switch cli.kind {
	case "vite", "next", "astro", "nuxt", "remix", "react-router":
		return cli.kind
	case "nest":
		return "node"
	case "node":
		if cli.listen == listenSource {
			return "node"
		}
	}
	return ""
}
