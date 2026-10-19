package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const kindMonorepo = "monorepo"

type pkgJSON struct {
	Name       string            `json:"name"`
	Main       string            `json:"main"`
	Scripts    map[string]string `json:"scripts"`
	Workspaces json.RawMessage   `json:"workspaces"`
}

// devScripts are the script names tried, in order, for a dev server.
var devScripts = []string{"dev", "start:dev", "serve", "start"}

func detectNode(dir string) (Suggestion, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return Suggestion{}, false
	}
	var pkg pkgJSON
	if json.Unmarshal(data, &pkg) != nil {
		return Suggestion{}, false
	}
	script := ""
	for _, s := range devScripts {
		if pkg.Scripts[s] != "" {
			script = s
			break
		}
	}
	if script == "" {
		return Suggestion{}, false
	}
	body := pkg.Scripts[script]
	pm := packageManager(dir)
	s := Suggestion{Cmd: pm.run(script), Kind: "node"}
	words, simple := serverCommand(body)
	cli := nodeCLI{kind: "node"}
	if simple {
		cli = nodeServer(words)
	}
	workspace := len(pkg.Workspaces) > 0 || exists(dir, "pnpm-workspace.yaml") || exists(dir, "turbo.json") || exists(dir, "nx.json") || exists(dir, "lerna.json")
	// A workspace root's script starts the apps, each on its own port.
	if simple && delegates(words) || workspace && cli.listen == listenNone {
		s.Kind, s.NoPort = kindMonorepo, true
		return s, true
	}
	s.Kind = cli.kind
	switch {
	case pinnedPort(body) > 0:
		// A script that pins its own port keeps it; the suggestion must use the same one.
		s.Port = pinnedPort(body)
	case portVarRe.MatchString(body):
	case cli.listen == listenFlags:
		s.Cmd += pm.sep() + cli.flags
	case cli.listen == listenEnv:
	case cli.listen == listenSource && readsNodePort(dir, pkg.Main, cli.entries, words[1:]):
	default:
		s.NoPort = true
	}
	return s, true
}

// listen says how a command can be told its port.
type listen int

const (
	listenNone   listen = iota // no known way
	listenFlags                // the flags in nodeCLI.flags
	listenEnv                  // it reads $PORT
	listenSource               // a runner whose entry file may read process.env.PORT
)

type nodeCLI struct {
	kind, flags string
	listen      listen
	entries     []string // conventional entry files for listenSource
}

var (
	viteFlags   = "--host 127.0.0.1 --port {PORT} --strictPort"
	hostPort    = "--host 127.0.0.1 --port {PORT}"
	nodeEntries = []string{"server.js", "server.ts", "index.js", "index.ts", "app.js", "app.ts",
		"src/server.ts", "src/server.js", "src/index.ts", "src/index.js", "src/main.ts", "src/app.ts", "src/app.js"}
)

// nodeServer recognizes the dev server CLIs whose port can be set, by the
// command and subcommand a script runs.
func nodeServer(words []string) nodeCLI {
	if len(words) == 0 {
		return nodeCLI{kind: "node"}
	}
	prog, sub := words[0], ""
	if len(words) > 1 {
		sub = words[1]
	}
	bare := sub == "" || strings.HasPrefix(sub, "-")
	flags := func(kind, flags string) nodeCLI { return nodeCLI{kind: kind, flags: flags, listen: listenFlags} }
	switch {
	case prog == "next" && (bare || sub == "dev" || sub == "start"):
		return flags("next", "--hostname 127.0.0.1 --port {PORT}")
	case prog == "vite" && (bare || sub == "dev" || sub == "serve" || sub == "preview"):
		return flags("vite", viteFlags)
	case prog == "astro" && (sub == "dev" || sub == "preview"):
		return flags("astro", hostPort)
	case (prog == "nuxt" || prog == "nuxi") && sub == "dev":
		return flags("nuxt", hostPort)
	case prog == "remix" && sub == "vite:dev":
		return flags("remix", viteFlags)
	case prog == "remix" && sub == "dev", prog == "remix-serve":
		return nodeCLI{kind: "remix", listen: listenEnv}
	case prog == "react-router" && sub == "dev":
		return flags("react-router", viteFlags)
	case prog == "react-router-serve":
		return nodeCLI{kind: "react-router", listen: listenEnv}
	case prog == "ng" && (sub == "serve" || sub == "s"):
		return flags("angular", hostPort)
	case prog == "gatsby" && sub == "develop":
		return flags("gatsby", "-H 127.0.0.1 -p {PORT}")
	case prog == "webpack" && sub == "serve", prog == "webpack-dev-server" && (bare || sub == "serve"):
		return flags("webpack", hostPort)
	case prog == "vue-cli-service" && sub == "serve":
		return flags("vue", hostPort)
	case prog == "docusaurus" && sub == "start":
		return flags("docusaurus", hostPort)
	case (prog == "react-scripts" || prog == "craco") && sub == "start":
		return nodeCLI{kind: "cra", listen: listenEnv}
	case prog == "wrangler" && sub == "dev":
		return flags("wrangler", "--ip 127.0.0.1 --port {PORT}")
	case prog == "nest" && sub == "start":
		return nodeCLI{kind: "nest", listen: listenSource, entries: []string{"src/main.ts"}}
	case slices.Contains([]string{"node", "tsx", "ts-node", "ts-node-dev", "nodemon", "node-dev", "bun"}, prog):
		return nodeCLI{kind: "node", listen: listenSource, entries: nodeEntries}
	}
	return nodeCLI{kind: "node"}
}

var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// serverCommand returns the words of the command a script leaves running: the
// last of an && chain, without environment assignments and launchers such as
// npx or cross-env. simple is false for scripts that run commands in the
// background, in pipes, in sequence with ; or ||, which cannot take flags.
func serverCommand(body string) (words []string, simple bool) {
	if strings.ContainsAny(strings.ReplaceAll(body, "&&", ""), "&|;") {
		return nil, false
	}
	segments := strings.Split(body, "&&")
	words = strings.Fields(segments[len(segments)-1])
	for len(words) > 0 {
		w := words[0]
		switch {
		case envAssignRe.MatchString(w):
			words = words[1:]
		case w == "cross-env" || w == "env" || w == "npx" || w == "pnpx" || w == "bunx":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		case (w == "pnpm" || w == "yarn") && len(words) > 1 && (words[1] == "exec" || words[1] == "dlx"):
			words = words[2:]
		case w == "dotenv" || w == "dotenv-cli":
			i := slices.Index(words, "--")
			if i < 0 {
				return nil, false
			}
			words = words[i+1:]
		default:
			words[0] = filepath.Base(words[0])
			return words, true
		}
	}
	return nil, false
}

// delegates reports whether a command runs scripts of workspace packages.
func delegates(words []string) bool {
	if len(words) == 0 {
		return false
	}
	args := words[1:]
	anyArg := func(names ...string) bool {
		return slices.ContainsFunc(args, func(a string) bool {
			for _, n := range names {
				if a == n || strings.HasPrefix(a, n+"=") {
					return true
				}
			}
			return false
		})
	}
	switch words[0] {
	case "turbo", "nx", "lerna":
		return true
	case "pnpm":
		return anyArg("-r", "--recursive", "--filter", "-F", "--parallel")
	case "yarn":
		return len(args) > 0 && (args[0] == "workspaces" || args[0] == "workspace")
	case "npm":
		return anyArg("--workspaces", "-ws", "--workspace", "-w")
	case "bun":
		return anyArg("--filter")
	}
	return false
}

var (
	scriptPortRe = regexp.MustCompile(`(?:--port[= ]|\s-p\s+|\bPORT=)(\d{2,5})\b`)
	portVarRe    = regexp.MustCompile(`\{PORT\}|\$\{?PORT\b`)
	nodePortRe   = regexp.MustCompile(`process\.env\.PORT\b|process\.env\[["']PORT["']\]|Bun\.env\.PORT\b`)
	scriptFileRe = regexp.MustCompile(`\.(?:[cm]?js|[cm]?ts|tsx)$`)
)

// pinnedPort returns the port a script sets itself, or 0.
func pinnedPort(body string) int {
	m := scriptPortRe.FindStringSubmatch(body)
	if m == nil {
		return 0
	}
	p, _ := strconv.Atoi(m[1])
	return p
}

// readsNodePort reports whether the entry file of a runner script reads PORT
// from the environment: a file named in its arguments, else package.json
// main, else a conventional entry file.
func readsNodePort(dir, main string, entries, args []string) bool {
	var files []string
	for _, a := range args {
		if scriptFileRe.MatchString(a) {
			files = append(files, a)
		}
	}
	if len(files) == 0 && main != "" {
		files = append(files, main)
	}
	if len(files) == 0 {
		files = entries
	}
	return slices.ContainsFunc(files, func(f string) bool { return fileMatches(dir, f, nodePortRe) })
}

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

// detectDeno proposes the dev task of a deno.json or deno.jsonc. deno task
// appends extra arguments to the task, so Vite tasks get Vite's flags.
func detectDeno(dir string) (Suggestion, bool) {
	var cfg struct {
		Tasks map[string]json.RawMessage `json:"tasks"`
	}
	found := false
	for _, f := range []string{"deno.json", "deno.jsonc"} {
		if data, err := os.ReadFile(filepath.Join(dir, f)); err == nil && json.Unmarshal(stripJSONC(data), &cfg) == nil {
			found = true
			break
		}
	}
	if !found {
		return Suggestion{}, false
	}
	for _, task := range []string{"dev", "start", "serve"} {
		raw, ok := cfg.Tasks[task]
		if !ok {
			continue
		}
		var body string
		if json.Unmarshal(raw, &body) != nil {
			var obj struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(raw, &obj) != nil || obj.Command == "" {
				continue
			}
			body = obj.Command
		}
		s := Suggestion{Kind: "deno", Cmd: "deno task " + task}
		words, simple := serverCommand(body)
		switch {
		case pinnedPort(body) > 0:
			s.Port = pinnedPort(body)
		case portVarRe.MatchString(body):
		case simple && nodeServer(words).kind == "vite":
			s.Kind, s.Cmd = "vite", s.Cmd+" "+viteFlags
		default:
			s.NoPort = true
		}
		return s, true
	}
	return Suggestion{}, false
}

// stripJSONC removes comments and trailing commas outside strings, which
// deno.jsonc allows and encoding/json does not.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := strings.Index(string(data[i+2:]), "*/")
			if end < 0 {
				return out
			}
			i += end + 3
		case c == ',':
			j := i + 1
			for j < len(data) && strings.IndexByte(" \t\r\n", data[j]) >= 0 {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
