package discover

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/padmk/portside/internal/config"
)

// detectBackend proposes the application servers of web frameworks outside
// Node. They come before a Node script in the same folder, which then usually
// serves assets.
func detectBackend(dir string, has Installed) []Suggestion {
	var out []Suggestion
	if exists(dir, "artisan") && fileMatches(dir, "composer.json", laravelRe) {
		out = append(out, Suggestion{Kind: "laravel", Cmd: "php artisan serve --host=127.0.0.1 --port={PORT}"})
	}
	if exists(dir, "Gemfile") && exists(dir, "bin/rails") {
		out = append(out, Suggestion{Kind: "rails", Cmd: "bin/rails server -b 127.0.0.1 -p {PORT}"})
	}
	out = append(out, detectDjango(dir, has)...)
	if s, ok := detectPhoenix(dir); ok {
		out = append(out, s)
	}
	if s, ok := detectSpring(dir); ok {
		out = append(out, s)
	}
	if s, ok := detectDotnet(dir); ok {
		out = append(out, s)
	}
	return out
}

var (
	laravelRe     = regexp.MustCompile(`"laravel/framework"`)
	phoenixRe     = regexp.MustCompile(`\{:phoenix,`)
	phoenixEnvRe  = regexp.MustCompile(`System\.get_env\("PORT"\)`)
	phoenixPortRe = regexp.MustCompile(`\bport:\s*(\d{2,5})\b`)
	springWebRe   = regexp.MustCompile(`spring-boot-starter-web(?:flux)?\b`)
	mavenBootRe   = regexp.MustCompile(`spring-boot-maven-plugin`)
	gradleBootRe  = regexp.MustCompile(`org\.springframework\.boot`)
	dotnetWebRe   = regexp.MustCompile(`Sdk="Microsoft\.NET\.Sdk\.Web"`)
)

// detectPhoenix proposes mix phx.server. Generated dev configs read PORT;
// older ones pin a port, which the suggestion keeps.
func detectPhoenix(dir string) (Suggestion, bool) {
	if !fileMatches(dir, "mix.exs", phoenixRe) {
		return Suggestion{}, false
	}
	s := Suggestion{Kind: "phoenix", Cmd: "mix phx.server"}
	data, _ := os.ReadFile(filepath.Join(dir, "config", "dev.exs"))
	if phoenixEnvRe.Match(data) {
		return s, true
	}
	if m := phoenixPortRe.FindSubmatch(data); m != nil {
		s.Port, _ = strconv.Atoi(string(m[1]))
	} else {
		s.NoPort = true
	}
	return s, true
}

// detectSpring proposes a Spring Boot web application through its build
// tool's wrapper when present; command-line properties override the
// application's own port.
func detectSpring(dir string) (Suggestion, bool) {
	tool := func(wrapper, plain string) string {
		if exists(dir, wrapper) {
			return "./" + wrapper
		}
		return plain
	}
	if fileMatches(dir, "pom.xml", mavenBootRe) && fileMatches(dir, "pom.xml", springWebRe) {
		return Suggestion{Kind: "spring", Cmd: tool("mvnw", "mvn") + ` spring-boot:run -Dspring-boot.run.arguments="--server.address=127.0.0.1 --server.port={PORT}"`}, true
	}
	for _, f := range []string{"build.gradle", "build.gradle.kts"} {
		if fileMatches(dir, f, gradleBootRe) && fileMatches(dir, f, springWebRe) {
			return Suggestion{Kind: "spring", Cmd: tool("gradlew", "gradle") + ` bootRun --args="--server.address=127.0.0.1 --server.port={PORT}"`}, true
		}
	}
	return Suggestion{}, false
}

// detectDotnet proposes an ASP.NET Core project when the folder holds exactly
// one project file, which dotnet run needs.
func detectDotnet(dir string) (Suggestion, bool) {
	projects, _ := filepath.Glob(filepath.Join(dir, "*.csproj"))
	if len(projects) != 1 || !fileMatches(dir, filepath.Base(projects[0]), dotnetWebRe) {
		return Suggestion{}, false
	}
	return Suggestion{Kind: "dotnet", Cmd: "dotnet run -- --urls http://127.0.0.1:{PORT}"}, true
}

var (
	goMainRe  = regexp.MustCompile(`(?m)^package main\b`)
	goServeRe = regexp.MustCompile(`\.ListenAndServe(?:TLS)?\(|"github\.com/gin-gonic/gin"|"github\.com/labstack/echo(?:/v\d+)?"|"github\.com/gofiber/fiber(?:/v\d+)?"`)
	goPortRe  = regexp.MustCompile(`(?:Getenv|LookupEnv)\("PORT"\)`)
	// safeSegmentRe matches folder names that may appear in a command.
	safeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
)

// detectGo proposes go run for main packages, in the module root or under
// cmd/, that visibly start an HTTP server; only those reading PORT get a port.
func detectGo(dir, name string) []Suggestion {
	if !exists(dir, "go.mod") {
		return nil
	}
	var out []Suggestion
	if s, ok := goServer(dir, "."); ok {
		out = append(out, s)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "cmd"))
	for _, e := range entries {
		// The folder name goes into the command, so only plain names do.
		if e.IsDir() && safeSegmentRe.MatchString(e.Name()) {
			if s, ok := goServer(dir, "./cmd/"+e.Name()); ok {
				s.Name = Slug(name + "-" + e.Name())
				out = append(out, s)
			}
		}
	}
	return out
}

func goServer(dir, pkg string) (Suggestion, bool) {
	files, _ := filepath.Glob(filepath.Join(dir, pkg, "*.go"))
	main, serves, port := false, false, false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil || !goMainRe.Match(data) {
			continue
		}
		main = true
		serves = serves || goServeRe.Match(data)
		port = port || goPortRe.Match(data)
	}
	if !main || !serves {
		return Suggestion{}, false
	}
	return Suggestion{Kind: "go", Cmd: "go run " + shellQuote(pkg), NoPort: !port}, true
}

var (
	rustWebCrates = []string{"axum", "actix-web", "rocket", "warp", "poem", "salvo", "tide"}
	rustPortRe    = regexp.MustCompile(`env::var\("PORT"\)`)
)

// detectRust proposes cargo run for a binary crate depending on a web
// framework; only one reading PORT in src/main.rs gets a port.
func detectRust(dir string) (Suggestion, bool) {
	var cargo struct {
		Package      map[string]any `toml:"package"`
		Dependencies map[string]any `toml:"dependencies"`
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "Cargo.toml"), &cargo); err != nil || cargo.Package == nil || !exists(dir, "src/main.rs") {
		return Suggestion{}, false
	}
	for _, c := range rustWebCrates {
		if _, ok := cargo.Dependencies[c]; ok {
			return Suggestion{Kind: "rust", Cmd: "cargo run", NoPort: !fileMatches(dir, "src/main.rs", rustPortRe)}, true
		}
	}
	return Suggestion{}, false
}

// detectCompose proposes docker compose up. Managed Compose commands get
// COMPOSE_MENU=false in their environment, so no menu flag is needed, which
// older Compose versions and podman-compose would reject.
func detectCompose(dir string) (Suggestion, bool) {
	compose := composeFile(dir)
	if compose == "" {
		return Suggestion{}, false
	}
	s := Suggestion{Kind: "compose", StopAfter: config.ComposeStop, Cmd: "docker compose up"}
	if compose != "compose.yaml" && compose != "compose.yml" && compose != "docker-compose.yml" && compose != "docker-compose.yaml" {
		s.Cmd = "docker compose -f " + compose + " up"
	}
	if p := composePort(filepath.Join(dir, compose)); p > 0 {
		s.Port = p
	} else {
		s.NoPort = true
	}
	return s, true
}

func composeFile(dir string) string {
	for _, f := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml", "compose.dev.yaml", "compose.dev.yml", "docker-compose.dev.yml", "docker-compose.dev.yaml"} {
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
