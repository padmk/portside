package discover

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var turboExtendsRe = regexp.MustCompile(`"extends"\s*:`)

// workspaceRoot reports whether dir is the root of a JavaScript workspace
// and returns its package folders. A root declares packages in package.json
// workspaces or pnpm-workspace.yaml packages; a turbo.json (not a package's
// own, which extends the root's), nx.json or lerna.json counts only when
// packages exist where the tool looks for them.
func workspaceRoot(dir string, pkg pkgJSON) (bool, []string) {
	globs := slices.Concat(packageJSONWorkspaces(pkg.Workspaces), pnpmWorkspacePackages(dir))
	declared := len(globs) > 0
	tool := exists(dir, "turbo.json") && !fileMatches(dir, "turbo.json", turboExtendsRe) || exists(dir, "nx.json") || exists(dir, "lerna.json")
	if !declared && tool {
		globs = lernaPackages(dir)
		if len(globs) == 0 {
			globs = []string{"apps/*", "packages/*"}
		}
	}
	packages := matchPackages(dir, globs)
	return declared || tool && len(packages) > 0, packages
}

// packageJSONWorkspaces reads package.json workspaces, a list of globs or an
// object with packages.
func packageJSONWorkspaces(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	json.Unmarshal(raw, &obj)
	return obj.Packages
}

func lernaPackages(dir string) []string {
	var cfg struct {
		Packages []string `json:"packages"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "lerna.json"))
	if err != nil || json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	return cfg.Packages
}

// pnpmWorkspacePackages reads the packages list of pnpm-workspace.yaml, in
// block or flow style. The file may hold only settings.
func pnpmWorkspacePackages(dir string) []string {
	f, err := os.Open(filepath.Join(dir, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}
	defer f.Close()
	unquote := func(s string) string {
		s = strings.TrimSpace(s)
		if i := strings.Index(s, " #"); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		return strings.Trim(s, `"'`)
	}
	var out []string
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' && line[0] != '-' {
			in = false
			rest, ok := strings.CutPrefix(line, "packages:")
			if !ok {
				continue
			}
			rest = unquote(rest)
			if inline, ok := strings.CutPrefix(rest, "["); ok {
				for _, p := range strings.Split(strings.TrimSuffix(inline, "]"), ",") {
					if p = unquote(p); p != "" {
						out = append(out, p)
					}
				}
				continue
			}
			in = true
			continue
		}
		if item, ok := strings.CutPrefix(trimmed, "-"); in && ok {
			if p := unquote(item); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// matchPackages returns the folders below dir with a package.json that the
// workspace globs match. Negated globs and globs leaving dir are ignored; **
// matches one or two levels.
func matchPackages(dir string, globs []string) []string {
	var out []string
	// Folders reached twice through symlinks, or a symlink back to dir,
	// count once.
	seen := map[string]bool{realPath(dir): true}
	for _, g := range globs {
		g = strings.Trim(strings.TrimPrefix(filepath.ToSlash(g), "./"), "/")
		if g == "" || strings.HasPrefix(g, "!") || slices.Contains(strings.Split(g, "/"), "..") {
			continue
		}
		patterns := []string{strings.ReplaceAll(g, "**", "*")}
		if strings.Contains(g, "**") {
			patterns = append(patterns, strings.ReplaceAll(g, "**", "*/*"))
		}
		for _, p := range patterns {
			matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p)))
			for _, m := range matches {
				rel, err := filepath.Rel(dir, m)
				if err != nil || rel == "." || slices.ContainsFunc(strings.Split(rel, string(filepath.Separator)), func(s string) bool {
					return strings.HasPrefix(s, ".") || skipDirs[s]
				}) {
					continue
				}
				if real := realPath(m); exists(m, "package.json") && !seen[real] {
					seen[real] = true
					out = append(out, m)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
