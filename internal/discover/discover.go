// Package discover finds projects that look like they have a dev server and
// proposes a command for each, so adding a server rarely means typing one.
//
// Detection is conservative, because a wrong suggestion is worse than none: a
// suggestion gets an automatic port only when its command is known to listen
// on {PORT} or $PORT, and a project kind is proposed only on clear evidence.
// Node and Deno scripts live in node.go, Python in python.go, other
// ecosystems and Compose in backends.go.
package discover

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

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
		case "--port", "-p", "--host", "--hostname", "-h", "-H", "-b", "--ip", "--urls", "--menu":
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

// A project root with a dev server rarely nests another worth listing, except
// in these monorepo folders, which are still searched.
var nestedDirs = map[string]bool{"apps": true, "packages": true, "web": true, "frontend": true, "services": true}

// Scan walks root up to depth levels deep and returns suggestions sorted by
// directory. Directories in skip (absolute paths) are left out.
func Scan(root string, depth int, skip map[string]bool) []Suggestion {
	var walk func(dir string, level int) []Suggestion
	walk = func(dir string, level int) []Suggestion {
		if skip[dir] {
			return nil
		}
		found := detect(dir)
		var below []Suggestion
		if entries, err := os.ReadDir(dir); err == nil && level < depth {
			for _, e := range entries {
				n := e.Name()
				if !e.IsDir() || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || skipDirs[n] {
					continue
				}
				if len(found) > 0 && !nestedDirs[n] {
					continue
				}
				below = append(below, walk(filepath.Join(dir, n), level+1)...)
			}
		}
		// A workspace root's command runs the apps below it, which are
		// proposed one by one instead.
		if len(below) > 0 {
			found = slices.DeleteFunc(found, func(s Suggestion) bool { return s.Kind == kindMonorepo })
		}
		return append(found, below...)
	}
	out := walk(root, 0)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
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

// fileMatches reports whether the file dir/name exists and matches re.
func fileMatches(dir, name string, re *regexp.Regexp) bool {
	data, err := os.ReadFile(filepath.Join(dir, name))
	return err == nil && re.Match(data)
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

// detect proposes the servers of one directory. The application server comes
// first and keeps the directory's name; asset servers and other entry points
// get their kind as a suffix, Compose services "-services".
func detect(dir string) []Suggestion {
	name := nameFor(dir)
	var out []Suggestion
	add := func(found ...Suggestion) {
		for _, s := range found {
			s.Dir = dir
			if s.Name == "" {
				s.Name = name
			}
			if slices.ContainsFunc(out, func(o Suggestion) bool { return o.Name == s.Name }) {
				s.Name = Slug(s.Name + "-" + s.Kind)
			}
			out = append(out, s)
		}
	}
	add(detectBackend(dir)...)
	if s, ok := detectNode(dir); ok {
		add(s)
	} else if s, ok := detectDeno(dir); ok {
		add(s)
	}
	if !exists(dir, "manage.py") {
		add(detectPython(dir, name)...)
	}
	add(detectGo(dir, name)...)
	if s, ok := detectRust(dir); ok {
		add(s)
	}
	if s, ok := detectCompose(dir); ok {
		if len(out) > 0 {
			s.Name = name + "-services"
		}
		add(s)
	}
	return out
}
