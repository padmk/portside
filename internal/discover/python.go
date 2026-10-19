package discover

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// pyRunner runs Python commands the way the project installs its packages.
type pyRunner struct {
	prefix string // tool runner such as "uv run ", empty for a plain interpreter
	python string // interpreter command
	bin    string // virtual environment bin directory, with a trailing slash
}

// pythonRunner chooses the runner by lockfile (in the project or a parent up
// to the repository root), then a virtual environment, then python3.
func pythonRunner(dir string) pyRunner {
	for d := dir; ; d = filepath.Dir(d) {
		switch {
		case exists(d, "uv.lock"):
			return pyRunner{prefix: "uv run ", python: "uv run python"}
		case exists(d, "poetry.lock"):
			return pyRunner{prefix: "poetry run ", python: "poetry run python"}
		case exists(d, "Pipfile.lock"), exists(d, "Pipfile"):
			return pyRunner{prefix: "pipenv run ", python: "pipenv run python"}
		case exists(d, "pdm.lock"):
			return pyRunner{prefix: "pdm run ", python: "pdm run python"}
		}
		if parent := filepath.Dir(d); parent == d || exists(d, ".git") {
			break
		}
	}
	for _, venv := range []string{".venv", "venv"} {
		if exists(dir, venv+"/bin/python") {
			return pyRunner{python: venv + "/bin/python", bin: venv + "/bin/"}
		}
	}
	return pyRunner{python: "python3"}
}

// module runs an installed Python module or tool such as uvicorn.
func (r pyRunner) module(name string) string {
	if r.prefix != "" {
		return r.prefix + name
	}
	return r.python + " -m " + name
}

// script runs a declared entry point; ok is false when no environment
// installs it.
func (r pyRunner) script(name string) (string, bool) {
	switch {
	case r.prefix != "":
		return r.prefix + name, true
	case r.bin != "":
		return r.bin + name, true
	}
	return "", false
}

var djangoRe = regexp.MustCompile(`(?i)django`)

func detectDjango(dir string) []Suggestion {
	if !fileMatches(dir, "manage.py", djangoRe) {
		return nil
	}
	return []Suggestion{{Kind: "django", Cmd: pythonRunner(dir).python + " manage.py runserver 127.0.0.1:{PORT}"}}
}

func detectPython(dir, name string) []Suggestion {
	var project struct {
		Project struct {
			Dependencies []string          `toml:"dependencies"`
			Scripts      map[string]string `toml:"scripts"`
		}
		DependencyGroups map[string][]any `toml:"dependency-groups"`
		Tool             struct {
			Poetry struct {
				Dependencies map[string]any `toml:"dependencies"`
				Group        map[string]struct {
					Dependencies map[string]any `toml:"dependencies"`
				} `toml:"group"`
			} `toml:"poetry"`
		} `toml:"tool"`
	}
	_, err := toml.DecodeFile(filepath.Join(dir, "pyproject.toml"), &project)
	hasProject := err == nil
	deps := map[string]bool{}
	addDep := func(req string) {
		if n := depNameRe.FindString(strings.TrimSpace(req)); n != "" {
			deps[strings.ReplaceAll(strings.ToLower(n), "_", "-")] = true
		}
	}
	for _, d := range project.Project.Dependencies {
		addDep(d)
	}
	for _, group := range project.DependencyGroups {
		for _, d := range group {
			if s, ok := d.(string); ok {
				addDep(s)
			}
		}
	}
	for n := range project.Tool.Poetry.Dependencies {
		addDep(n)
	}
	for _, g := range project.Tool.Poetry.Group {
		for n := range g.Dependencies {
			addDep(n)
		}
	}
	hasRequirements := readRequirements(filepath.Join(dir, "requirements.txt"), addDep)
	var pipfile struct {
		Packages    map[string]any `toml:"packages"`
		DevPackages map[string]any `toml:"dev-packages"`
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "Pipfile"), &pipfile); err == nil {
		hasRequirements = true
		for n := range pipfile.Packages {
			addDep(n)
		}
		for n := range pipfile.DevPackages {
			addDep(n)
		}
	}
	if !hasProject && !hasRequirements {
		return nil
	}
	run := pythonRunner(dir)
	var out []Suggestion
	if deps["fastapi"] || deps["starlette"] || deps["uvicorn"] {
		// Only conventional modules with an explicit ASGI app are proposed.
		for _, module := range []string{"main", "app", "api.main", "app.main", "server.main", "src.main"} {
			if fileMatches(dir, strings.ReplaceAll(module, ".", "/")+".py", asgiAppRe) {
				out = append(out, Suggestion{Name: name, Kind: "python", Cmd: run.module("uvicorn") + " " + module + ":app --reload --host 127.0.0.1 --port {PORT}"})
				break
			}
		}
	}
	if deps["flask"] {
		// Modules Flask itself would find: an app or application object, or a factory.
		for _, module := range []string{"app", "wsgi", "main", "server", "application"} {
			if fileMatches(dir, module+".py", flaskAppRe) || fileMatches(dir, module+"/__init__.py", flaskAppRe) {
				out = append(out, Suggestion{Name: name, Kind: "flask", Cmd: run.module("flask") + " --app " + module + " run -h 127.0.0.1 -p {PORT}"})
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
		cmd, ok := run.script(script)
		if !ok {
			continue
		}
		// Entry points do not imply support for a port flag or even a listener.
		out = append(out, Suggestion{Name: Slug(name + "-" + script), Kind: "python", Cmd: cmd, NoPort: true})
	}
	return out
}

// readRequirements passes each requirement of a requirements file to add and
// reports whether the file exists.
func readRequirements(path string, add func(string)) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "-") {
			add(line)
		}
	}
	return true
}

var (
	depNameRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]+`)
	asgiAppRe      = regexp.MustCompile(`(?m)^app\s*(?::[^=\n]+)?=\s*(?:FastAPI|Starlette)\s*\(`)
	flaskAppRe     = regexp.MustCompile(`(?m)^(?:(?:app|application)\s*(?::[^=\n]+)?=\s*Flask\s*\(|def\s+(?:create_app|make_app)\s*\()`)
	pythonScriptRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	pythonEntryRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*:[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)
