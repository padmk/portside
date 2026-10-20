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
	Name            string            `json:"name"`
	Main            string            `json:"main"`
	Scripts         map[string]string `json:"scripts"`
	Workspaces      json.RawMessage   `json:"workspaces"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func readPackageJSON(dir string) (pkgJSON, bool) {
	var pkg pkgJSON
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	return pkg, err == nil && json.Unmarshal(data, &pkg) == nil
}

// devScripts are the script names tried, in order, for a dev server.
var devScripts = []string{"dev", "start:dev", "serve", "start"}

func detectNode(dir string) (Suggestion, bool) {
	pkg, ok := readPackageJSON(dir)
	if !ok {
		return Suggestion{}, false
	}
	name := ""
	for _, s := range devScripts {
		if pkg.Scripts[s] != "" {
			name = s
			break
		}
	}
	if name == "" {
		return Suggestion{}, false
	}
	pm := packageManager(dir)
	s := Suggestion{Cmd: pm.run(name), Kind: "node"}
	run := resolveScript(pkg.Scripts, pkg.Scripts[name])
	cli := nodeCLI{kind: "node"}
	if run.simple {
		cli = nodeServer(run.words, pkg)
	}
	if run.delegates {
		s.Kind, s.NoPort = kindMonorepo, true
		return s, true
	}
	pinned := pinnedPort(run.text)
	if cli.listen == listenSource {
		files := entryFiles(pkg.Main, cli.entries, run.words[1:])
		switch {
		case slices.ContainsFunc(files, func(f string) bool { return fileMatches(dir, f, nodePortRe) }):
		case slices.ContainsFunc(files, func(f string) bool { return fileMatches(dir, f, nextWrapperRe) }):
			// A wrapper that spawns Next.js passes its environment on, and
			// Next.js reads PORT, unless the wrapper passes its own port.
			cli = nodeCLI{kind: "next", listen: listenEnv}
			if p := wrapperPort(dir, files); p > 0 {
				pinned = p
			}
		default:
			cli.listen = listenNone
		}
	}
	// Flags added to the suggested command reach the CLI only when every
	// script on the way forwards its arguments.
	if cli.listen == listenFlags && !run.forwards {
		cli.listen = listenNone
		if cli.readsPort {
			cli.listen = listenEnv
		}
	}
	// A workspace root's script starts the apps, each on its own port.
	if workspace, _ := workspaceRoot(dir, pkg); workspace && cli.listen == listenNone {
		s.Kind, s.NoPort = kindMonorepo, true
		return s, true
	}
	s.Kind = cli.kind
	switch {
	case pinned > 0:
		// A script that pins its own port keeps it; the suggestion must use the same one.
		s.Port = pinned
	case portVarRe.MatchString(run.text):
	case cli.listen == listenFlags:
		s.Cmd += pm.sep() + cli.flags
	case cli.listen == listenEnv, cli.listen == listenSource:
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
	readsPort   bool     // with listenFlags: it also reads $PORT
	entries     []string // conventional entry files for listenSource
}

var (
	viteFlags   = "--host 127.0.0.1 --port {PORT} --strictPort"
	hostPort    = "--host 127.0.0.1 --port {PORT}"
	nodeEntries = []string{"server.js", "server.ts", "index.js", "index.ts", "app.js", "app.ts",
		"src/server.ts", "src/server.js", "src/index.ts", "src/index.js", "src/main.ts", "src/app.ts", "src/app.js"}
	nuxt2Re = regexp.MustCompile(`^[\^~>=v\s]*2(?:\.|\s|$)`)
)

// nodeServer recognizes the dev server CLIs whose port can be set, by the
// command and subcommand a script runs.
func nodeServer(words []string, pkg pkgJSON) nodeCLI {
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
		return nodeCLI{kind: "next", flags: "--hostname 127.0.0.1 --port {PORT}", listen: listenFlags, readsPort: true}
	case prog == "vite" && (bare || sub == "dev" || sub == "serve" || sub == "preview"):
		return flags("vite", viteFlags)
	case prog == "astro" && (sub == "dev" || sub == "preview"):
		return flags("astro", hostPort)
	case prog == "nuxt" && (bare || sub == "dev" && nuxt2(pkg)):
		// Nuxt 2, whose bare nuxt starts the dev server.
		return flags("nuxt", "--hostname 127.0.0.1 --port {PORT}")
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

// nuxt2 reports whether the package depends on Nuxt 2, whose CLI takes
// --hostname where later versions take --host.
func nuxt2(pkg pkgJSON) bool {
	v := pkg.Dependencies["nuxt"]
	if v == "" {
		v = pkg.DevDependencies["nuxt"]
	}
	return nuxt2Re.MatchString(v)
}

// script is the command a package script ends up running.
type script struct {
	words     []string // the last command of its && chain, without launchers
	simple    bool     // see serverCommand
	delegates bool     // it runs scripts of workspace packages
	forwards  bool     // arguments given to the first script reach words
	text      string   // the bodies of every script on the way
}

const maxScriptDepth = 8

// resolveScript follows calls of other scripts of the same package.json
// (npm run, pnpm, yarn, bun run, node --run), at most maxScriptDepth deep and
// never into a script already visited.
func resolveScript(scripts map[string]string, body string) script {
	s := script{forwards: true}
	seen := map[string]bool{}
	for range maxScriptDepth {
		s.text += body + "\n"
		s.words, s.simple = serverCommand(body, scripts)
		if !s.simple {
			return s
		}
		if delegates(s.words) {
			s.delegates = true
			return s
		}
		name, args, forwards := scriptCall(s.words, scripts)
		if name == "" || seen[name] {
			return s
		}
		seen[name] = true
		s.forwards = s.forwards && forwards
		body = scripts[name]
		if len(args) > 0 {
			body += " " + shellJoin(args)
		}
	}
	return s
}

// scriptCall returns the package script a command runs, the arguments passed
// to it and whether more arguments given to the command reach it as well.
func scriptCall(words []string, scripts map[string]string) (name string, args []string, forwards bool) {
	if len(words) < 2 {
		return "", nil, false
	}
	named := func(i int) bool { return i < len(words) && scripts[words[i]] != "" }
	// npm and node --run take options before "--" as their own.
	afterDashes := func(rest []string) ([]string, bool) {
		if i := slices.Index(rest, "--"); i >= 0 {
			return rest[i+1:], true
		}
		return slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return strings.HasPrefix(a, "-") }), false
	}
	runner := words[0] == "pnpm" || words[0] == "yarn" || words[0] == "bun"
	switch sub := words[1]; {
	case words[0] == "npm" && slices.Contains([]string{"run", "run-script", "rum", "urn"}, sub) && named(2),
		words[0] == "node" && sub == "--run" && named(2):
		args, forwards = afterDashes(words[3:])
		return words[2], args, forwards
	case words[0] == "npm" && sub == "start" && named(1):
		args, forwards = afterDashes(words[2:])
		return "start", args, forwards
	case runner && sub == "run" && named(2):
		return words[2], words[3:], true
	case runner && named(1):
		return sub, words[2:], true
	}
	return "", nil, false
}

// shellJoin quotes words for a shell where they need it.
func shellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = shellQuote(w)
	}
	return strings.Join(out, " ")
}

var shellSafeRe = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_{}-]+$`)

// shellQuote returns s as one shell word.
func shellQuote(s string) string {
	if shellSafeRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// pmCommands are package manager commands, which pnpm and yarn run instead
// of a package binary of the same name.
var pmCommands = map[string]bool{
	"add": true, "audit": true, "bin": true, "build": true, "cache": true, "config": true, "create": true,
	"dedupe": true, "deploy": true, "env": true, "fetch": true, "i": true, "info": true, "init": true,
	"install": true, "link": true, "list": true, "ls": true, "outdated": true, "pack": true, "patch": true,
	"prune": true, "publish": true, "rebuild": true, "remove": true, "rm": true, "run": true, "store": true,
	"test": true, "unlink": true, "up": true, "update": true, "upgrade": true, "why": true,
	"workspace": true, "workspaces": true,
}

// serverCommand returns the words of the command a script leaves running: the
// last of an && chain, without environment assignments and launchers such as
// npx, cross-env or a package manager running a package binary. simple is
// false for scripts that run commands in the background, in pipes, in
// sequence with ; or ||, or in subshells, which cannot take flags.
func serverCommand(body string, scripts map[string]string) (words []string, simple bool) {
	segments, ok := shellSegments(body)
	if !ok {
		return nil, false
	}
	words = segments[len(segments)-1]
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
		case (w == "pnpm" || w == "yarn" || w == "npm" || w == "bun") && len(words) > 1 && slices.Contains([]string{"exec", "dlx", "x"}, words[1]):
			words = words[2:]
			if len(words) > 0 && words[0] == "--" {
				words = words[1:]
			}
		case (w == "pnpm" || w == "yarn") && len(words) > 1 && !strings.HasPrefix(words[1], "-") && !pmCommands[words[1]] && scripts[words[1]] == "":
			// A package binary by name; resolveScript first takes a script
			// of that name.
			words = words[1:]
		case w == "bun" && len(words) > 2 && words[1] == "run" && !strings.HasPrefix(words[2], "-") && scripts[words[2]] == "":
			// bun run of a file or binary; scripts are resolved first.
			words = append([]string{"bun"}, words[2:]...)
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

// shellSegments splits a script into the words of each command of its &&
// chain, honoring quotes and backslashes. ok is false for other control
// operators, redirections, subshells and command substitution.
func shellSegments(body string) (segments [][]string, ok bool) {
	var words []string
	var word strings.Builder
	inWord := false
	end := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(body[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			word.WriteString(body[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			for i++; i < len(body) && body[i] != '"'; i++ {
				switch {
				case body[i] == '`', body[i] == '$' && i+1 < len(body) && body[i+1] == '(':
					return nil, false
				case body[i] == '\\' && i+1 < len(body) && strings.IndexByte("\"\\$`", body[i+1]) >= 0:
					i++
				}
				word.WriteByte(body[i])
			}
			if i >= len(body) {
				return nil, false
			}
			inWord = true
		case c == '\\' && i+1 < len(body):
			i++
			if body[i] != '\n' {
				word.WriteByte(body[i])
				inWord = true
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			end()
		case c == '&' && i+1 < len(body) && body[i+1] == '&':
			end()
			segments = append(segments, words)
			words = nil
			i++
		case strings.IndexByte("&|;()`<>", c) >= 0, c == '$' && i+1 < len(body) && body[i+1] == '(':
			return nil, false
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	end()
	return append(segments, words), true
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
	scriptPortRe  = regexp.MustCompile(`(?:--port[= ]|\s-p\s+|\bPORT=)(\d{2,5})\b`)
	portVarRe     = regexp.MustCompile(`\{PORT\}|\$\{?PORT\b`)
	nodePortRe    = regexp.MustCompile(`process\.env\.PORT\b|process\.env\[["']PORT["']\]|Bun\.env\.PORT\b`)
	nextWrapperRe = regexp.MustCompile(`["']next/dist/bin/next["']|\bspawn(?:Sync)?\(\s*["']next["']`)
	scriptFileRe  = regexp.MustCompile(`\.(?:[cm]?js|[cm]?ts|tsx)$`)
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

// wrapperPortRe finds a literal port flag in a Next.js wrapper's arguments,
// such as `"-p", "3000"`, `'--port=3000'` or `"next dev -p 3000"`.
var wrapperPortRe = regexp.MustCompile(`(?:^|["'\s,\[\x60])(?:-p|--port)(?:["']\s*,\s*["']?|[=\s]+)(\d{2,5})\b`)

// wrapperPort returns the port a Next.js wrapper passes to next itself, or 0.
func wrapperPort(dir string, files []string) int {
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || !nextWrapperRe.Match(data) {
			continue
		}
		if m := wrapperPortRe.FindSubmatch(data); m != nil {
			if p, _ := strconv.Atoi(string(m[1])); p > 0 && p < 65536 {
				return p
			}
		}
	}
	return 0
}

// entryFiles returns the entry files of a runner script: the files named in
// its arguments, else package.json main, else the conventional entries.
func entryFiles(main string, entries, args []string) []string {
	var files []string
	for _, a := range args {
		if scriptFileRe.MatchString(a) {
			files = append(files, a)
		}
	}
	switch {
	case len(files) > 0:
		return files
	case main != "":
		return []string{main}
	}
	return entries
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
		words, simple := serverCommand(body, nil)
		switch {
		case pinnedPort(body) > 0:
			s.Port = pinnedPort(body)
		case portVarRe.MatchString(body):
		case simple && nodeServer(words, pkgJSON{}).kind == "vite":
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
			if j := skipJSONCSpace(data, i+1); j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// skipJSONCSpace returns the index of the first byte from i on that is
// neither whitespace nor inside a comment.
func skipJSONCSpace(data []byte, i int) int {
	for i < len(data) {
		switch {
		case strings.IndexByte(" \t\r\n", data[i]) >= 0:
			i++
		case data[i] == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case data[i] == '/' && i+1 < len(data) && data[i+1] == '*':
			end := strings.Index(string(data[i+2:]), "*/")
			if end < 0 {
				return len(data)
			}
			i += end + 4
		default:
			return i
		}
	}
	return i
}
