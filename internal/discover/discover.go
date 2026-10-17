// Package discover finds projects that look like they have a dev server and
// proposes a command for each, so adding a server rarely means typing one.
package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/ports"
)

// Suggestion is a proposed server definition.
type Suggestion struct {
	Name                string `json:"name"`
	Dir                 string `json:"dir"`
	Cmd                 string `json:"cmd"`
	Kind                string `json:"kind"` // e.g. "next", "vite", "compose"
	Port                int    `json:"port,omitempty"`
	NoPort              bool   `json:"noPort,omitempty"`
	StopAfter           string `json:"stopTimeout,omitempty"`
	DirectoryConfigured bool   `json:"directoryConfigured,omitempty"`
}

// Prepare removes configured commands, marks shared directories and assigns
// unique names and automatic ports, checking the roster and live listeners.
func Prepare(cfg *config.Config, found []Suggestion) []Suggestion {
	return prepare(cfg, found, ports.Listening)
}

func prepare(cfg *config.Config, found []Suggestion, listening func(int) bool) []Suggestion {
	dirs, names, taken := map[string]bool{}, map[string]bool{}, map[int]bool{}
	commands := map[string]bool{}
	identity := func(dir, cmd string) string { return filepath.Clean(dir) + "\x00" + commandIdentity(cmd) }
	for _, sv := range cfg.Servers {
		dirs[filepath.Clean(sv.Dir)], names[sv.Name], taken[sv.Port] = true, true, true
		commands[identity(sv.Dir, sv.Cmd)] = true
	}
	// Reserve fixed ports before handing out automatic ports, regardless of order.
	for _, sg := range found {
		if sg.Port > 0 {
			taken[sg.Port] = true
		}
	}
	out := []Suggestion{}
	for _, sg := range found {
		id := identity(sg.Dir, sg.Cmd)
		if commands[id] {
			continue
		}
		commands[id] = true
		sg.DirectoryConfigured = dirs[filepath.Clean(sg.Dir)]
		base := sg.Name
		for i := 2; names[sg.Name]; i++ {
			suffix := "-" + strconv.Itoa(i)
			sg.Name = strings.TrimRight(base[:min(len(base), 63-len(suffix))], "-") + suffix
		}
		names[sg.Name] = true
		if sg.Port == 0 && !sg.NoPort {
			sg.Port = cfg.FreePort(func(p int) bool { return taken[p] || listening(p) })
		}
		taken[sg.Port] = true
		out = append(out, sg)
	}
	return out
}

// Ignore the flags added by detection when identifying an existing service.
func commandIdentity(cmd string) string {
	fields := strings.Fields(cmd)
	compose := strings.Contains(cmd, "docker compose") || strings.Contains(cmd, "docker-compose") || strings.Contains(cmd, "podman compose")
	out := []string{}
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if compose {
			if f == "--menu" && i+1 < len(fields) {
				i++
				continue
			}
			if strings.HasPrefix(f, "--menu=") {
				continue
			}
			out = append(out, f)
			continue
		}
		switch f {
		case "--port", "-p", "--host", "--hostname", "--menu":
			if i+1 < len(fields) {
				i++
			}
			continue
		case "--", "--strictPort", "--reload":
			continue
		}
		if strings.HasPrefix(f, "--port=") || strings.HasPrefix(f, "--host=") || strings.HasPrefix(f, "--hostname=") || strings.HasPrefix(f, "--menu=") {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "out": true,
	"target": true, "coverage": true, "tmp": true, "venv": true, "__pycache__": true,
}

// Scan walks root up to depth levels deep and returns suggestions sorted by
// directory. Directories in skip (absolute paths) are left out.
func Scan(root string, depth int, skip map[string]bool) []Suggestion {
	var out []Suggestion
	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		if skip[dir] {
			return
		}
		found := detect(dir)
		out = append(out, found...)
		// A project root with a dev server rarely nests another worth listing,
		// except in monorepos (apps/*, packages/*), which we still descend.
		if level >= depth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || skipDirs[n] {
				continue
			}
			if len(found) > 0 && n != "apps" && n != "packages" && n != "web" && n != "frontend" && n != "services" {
				continue
			}
			walk(filepath.Join(dir, n), level+1)
		}
	}
	walk(root, 0)
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	dedupeNames(out)
	return out
}

func dedupeNames(s []Suggestion) {
	count := map[string]int{}
	for i := range s {
		base := s[i].Name
		count[base]++
		if count[base] > 1 {
			s[i].Name = base + "-" + strconv.Itoa(count[base])
		}
	}
}

var nonName = regexp.MustCompile(`[^a-z0-9-]+`)

// Slug turns any string into a valid server name.
func Slug(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "@")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = nonName.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	if s == "" {
		s = "server"
	}
	return s
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

type pkgJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

// generic directory names say nothing on their own ("web"), so they are
// prefixed with the parent directory ("shop-web").
var generic = map[string]bool{
	"web": true, "app": true, "apps": true, "frontend": true, "client": true, "site": true,
	"ui": true, "dashboard": true, "server": true, "api": true, "backend": true, "www": true,
	"docs": true, "admin": true, "services": true, "packages": true,
}

func nameFor(dir string) string {
	parts := strings.Split(filepath.Clean(dir), string(filepath.Separator))
	name := ""
	for i := len(parts) - 1; i >= 0; i-- {
		p := Slug(parts[i])
		if name == "" {
			name = p
		} else if !generic[p] {
			if generic[strings.SplitN(name, "-", 2)[0]] {
				name = p + "-" + name
			}
			break
		} else if p != "apps" && p != "packages" && p != "services" {
			name = p + "-" + name
		}
		if !generic[p] {
			break
		}
	}
	return name
}

func detect(dir string) []Suggestion {
	var out []Suggestion
	name := nameFor(dir)
	if s, ok := detectNode(dir, name); ok {
		out = append(out, s)
	}
	if len(out) == 0 && exists(dir, "manage.py") {
		run := "python"
		if exists(dir, "uv.lock") || exists(dir, "pyproject.toml") {
			run = "uv run python"
		}
		out = append(out, Suggestion{Name: name, Dir: dir, Kind: "django",
			Cmd: run + " manage.py runserver 127.0.0.1:{PORT}"})
	}
	if len(out) == 0 && exists(dir, "Gemfile") && exists(dir, "bin/rails") {
		out = append(out, Suggestion{Name: name, Dir: dir, Kind: "rails",
			Cmd: "bin/rails server -b 127.0.0.1 -p {PORT}"})
	}
	if !exists(dir, "manage.py") {
		out = append(out, detectPython(dir, name)...)
	}
	if compose := composeFile(dir); compose != "" {
		s := Suggestion{Name: name, Dir: dir, Kind: "compose", StopAfter: "30s",
			Cmd: "docker compose up --menu=false"}
		if compose != "compose.yaml" && compose != "compose.yml" && compose != "docker-compose.yml" && compose != "docker-compose.yaml" {
			s.Cmd = "docker compose -f " + compose + " up --menu=false"
		}
		if p := composePort(filepath.Join(dir, compose)); p > 0 {
			s.Port = p
		} else {
			s.NoPort = true
		}
		if len(out) > 0 {
			s.Name = name + "-services"
		}
		out = append(out, s)
	}
	return out
}

func detectPython(dir, name string) []Suggestion {
	var project struct {
		Project struct {
			Dependencies []string          `toml:"dependencies"`
			Scripts      map[string]string `toml:"scripts"`
		}
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "pyproject.toml"), &project); err != nil {
		return nil
	}
	var out []Suggestion
	web := false
	depName := regexp.MustCompile(`^[A-Za-z0-9_-]+`)
	for _, dep := range project.Project.Dependencies {
		n := strings.ToLower(depName.FindString(strings.TrimSpace(dep)))
		if n == "uvicorn" || n == "fastapi" {
			web = true
		}
	}
	if web {
		// Only conventional modules with an explicit ASGI app are proposed.
		for _, module := range []string{"main", "app", "api.main", "app.main", "server.main", "src.main"} {
			data, err := os.ReadFile(filepath.Join(dir, strings.ReplaceAll(module, ".", "/")+".py"))
			if err == nil && asgiAppRe.Match(data) {
				out = append(out, Suggestion{Name: name, Dir: dir, Kind: "python", Cmd: "uv run uvicorn " + module + ":app --reload --host 127.0.0.1 --port {PORT}"})
				break
			}
		}
	}
	keys := make([]string, 0, len(project.Project.Scripts))
	for k := range project.Project.Scripts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, script := range keys {
		if !pythonScriptRe.MatchString(script) || !pythonEntryRe.MatchString(project.Project.Scripts[script]) {
			continue
		}
		// Entry points do not imply support for a port flag or even a listener.
		out = append(out, Suggestion{Name: Slug(name + "-" + script), Dir: dir, Kind: "python", Cmd: "uv run " + script, NoPort: true})
	}
	return out
}

var asgiAppRe = regexp.MustCompile(`(?m)^app\s*(?::[^=\n]+)?=\s*(?:FastAPI|Starlette)\s*\(`)
var pythonScriptRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var pythonEntryRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*:[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)

func composeFile(dir string) string {
	for _, f := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml", "compose.dev.yaml", "docker-compose.dev.yml"} {
		if exists(dir, f) {
			return f
		}
	}
	return ""
}

var composePortRe = regexp.MustCompile(`(?m)^\s*-\s*["']?(?:127\.0\.0\.1:|0\.0\.0\.0:)?(\d{2,5}):\d{2,5}`)

func composePort(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	m := composePortRe.FindSubmatch(data)
	if m == nil {
		return 0
	}
	p, _ := strconv.Atoi(string(m[1]))
	return p
}

func detectNode(dir, name string) (Suggestion, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return Suggestion{}, false
	}
	var pkg pkgJSON
	if json.Unmarshal(data, &pkg) != nil {
		return Suggestion{}, false
	}
	script := ""
	for _, s := range []string{"dev", "start:dev", "serve", "start"} {
		if pkg.Scripts[s] != "" {
			script = s
			break
		}
	}
	if script == "" {
		return Suggestion{}, false
	}
	has := func(dep string) bool { return pkg.Dependencies[dep] != "" || pkg.DevDependencies[dep] != "" }
	body := pkg.Scripts[script]
	kind, flags := "node", ""
	switch {
	case has("next") || strings.Contains(body, "next "):
		kind, flags = "next", "--hostname 127.0.0.1 --port {PORT}"
	case has("astro"):
		kind, flags = "astro", "--host 127.0.0.1 --port {PORT}"
	case has("nuxt"):
		kind, flags = "nuxt", "--host 127.0.0.1 --port {PORT}"
	case has("@remix-run/dev") && !has("vite"):
		kind = "remix"
	case has("vite") || strings.Contains(body, "vite"):
		kind, flags = "vite", "--host 127.0.0.1 --port {PORT} --strictPort"
	case strings.Contains(body, "turbo ") || len(pkg.Workspaces) > 0 && !strings.Contains(body, " --port"):
		kind = "monorepo"
	}
	pm := packageManager(dir)
	cmd := pm.run(script)
	// A script that pins its own port keeps it; the suggestion must use the same one.
	if m := scriptPortRe.FindStringSubmatch(body); m != nil {
		port, _ := strconv.Atoi(m[1])
		return Suggestion{Name: name, Dir: dir, Cmd: cmd, Kind: kind, Port: port}, true
	}
	if flags != "" {
		cmd += pm.sep() + flags
	}
	return Suggestion{Name: name, Dir: dir, Cmd: cmd, Kind: kind}, true
}

var scriptPortRe = regexp.MustCompile(`(?:--port[= ]|\s-p\s+)(\d{2,5})\b`)

type pm string

func packageManager(dir string) pm {
	for d := dir; ; d = filepath.Dir(d) {
		switch {
		case exists(d, "pnpm-lock.yaml"):
			return "pnpm"
		case exists(d, "bun.lockb"), exists(d, "bun.lock"):
			return "bun"
		case exists(d, "yarn.lock"):
			return "yarn"
		case exists(d, "package-lock.json"):
			return "npm"
		}
		if parent := filepath.Dir(d); parent == d || exists(d, ".git") {
			return "npm"
		}
	}
}

func (p pm) run(script string) string {
	switch p {
	case "pnpm":
		return "pnpm " + script
	case "yarn":
		return "yarn " + script
	case "bun":
		return "bun run " + script
	default:
		return "npm run " + script
	}
}

// sep separates script arguments; npm needs "--" to forward them.
func (p pm) sep() string {
	if p == "npm" {
		return " -- "
	}
	return " "
}
