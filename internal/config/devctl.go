package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/padmk/portside/internal/paths"
)

// DevctlImport is the result of reading a devctl servers.conf.
type DevctlImport struct {
	Root    string // ROOT=, with ~ kept for readability
	PathDir string // NODE_BIN=, if set
	Servers []Spec
	Skipped []string // human-readable reasons
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slug(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// ImportDevctl parses the devctl format (`name | dir | [port] | command`).
// Ports are computed exactly as devctl does, so every server keeps its port.
// Comment lines directly above an entry are carried over as its comment.
func ImportDevctl(path string) (*DevctlImport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	res := &DevctlImport{Root: "~/dev"}
	root := paths.ExpandHome("~/dev")
	base, step, idx := 3001, 100, 0
	confDir := filepath.Dir(path)
	if real, err := filepath.EvalSymlinks(confDir); err == nil {
		confDir = real
	}
	var pending []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			pending = nil
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			text := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			// Disabled entries and section rulers are not comments worth keeping.
			if strings.Contains(text, "|") || strings.HasPrefix(text, "---") || strings.HasPrefix(text, "===") {
				pending = nil
				continue
			}
			pending = append(pending, text)
			continue
		}
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if !strings.Contains(line, "|") {
			switch {
			case strings.HasPrefix(line, "ROOT="):
				v := strings.Trim(strings.TrimPrefix(line, "ROOT="), `"'`)
				v = strings.Replace(v, "$HOME", "~", 1)
				res.Root = v
				root = paths.ExpandHome(v)
			case strings.HasPrefix(line, "BASE="):
				base, _ = strconv.Atoi(strings.TrimPrefix(line, "BASE="))
			case strings.HasPrefix(line, "STEP="):
				step, _ = strconv.Atoi(strings.TrimPrefix(line, "STEP="))
			case strings.HasPrefix(line, "NODE_BIN="):
				res.PathDir = strings.Trim(strings.TrimPrefix(line, "NODE_BIN="), `"'`)
			}
			pending = nil
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		name, dir := parts[0], parts[1]
		var port int
		var cmd string
		if len(parts) == 4 {
			p, err := strconv.Atoi(parts[2])
			if err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s: invalid port %q", name, parts[2]))
				pending = nil
				continue
			}
			port, cmd = p, parts[3]
		} else if len(parts) == 3 {
			cmd = parts[2]
			port = base + idx*step
			for Reserved[port] {
				port++
			}
			idx++
		} else {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: not enough columns", name))
			pending = nil
			continue
		}
		absDir := paths.ExpandHome(dir)
		if !filepath.IsAbs(absDir) {
			absDir = filepath.Join(root, absDir)
		}
		if real, err := filepath.EvalSymlinks(absDir); err == nil && real == confDir {
			res.Skipped = append(res.Skipped, name+": devctl's own dashboard (replaced by this app)")
			pending = nil
			continue
		}
		if rel, err := filepath.Rel(root, absDir); err == nil && !strings.HasPrefix(rel, "..") {
			dir = rel
		} else {
			dir = paths.ShortenHome(absDir)
		}
		sp := Spec{
			Name:    slug(name),
			Dir:     dir,
			Cmd:     cmd,
			Port:    &port,
			Comment: strings.Join(pending, "\n"),
		}
		if strings.Contains(cmd, "docker compose") || strings.Contains(cmd, "docker-compose") {
			sp.Stop = "30s"
		}
		if sp.Name != name {
			sp.Comment = strings.TrimSpace(sp.Comment + "\nImported from devctl as " + name + ".")
		}
		res.Servers = append(res.Servers, sp)
		pending = nil
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	inferGroups(res.Servers)
	return res, nil
}

// inferGroups puts servers sharing a name prefix (zeus-web, zeus-api) in a group.
func inferGroups(specs []Spec) {
	count := map[string]int{}
	prefix := func(n string) string {
		if i := strings.IndexByte(n, '-'); i > 0 {
			return n[:i]
		}
		return n
	}
	for _, s := range specs {
		count[prefix(s.Name)]++
	}
	for i := range specs {
		if p := prefix(specs[i].Name); count[p] >= 2 {
			specs[i].Group = p
		}
	}
}
