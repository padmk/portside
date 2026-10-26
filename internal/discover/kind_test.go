package discover

import (
	"os"
	"path/filepath"
	"testing"
)

// The tool behind a configured command decides the fix the dashboard shows
// for a listener reachable from the network, so each must be told apart by
// what the command runs, package scripts included.
func TestCommandKind(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"scripts": {
		"dev": "vite --host",
		"next": "next dev",
		"astro": "astro dev --host",
		"nuxt": "nuxi dev --host",
		"remix": "remix vite:dev --host",
		"rr": "react-router dev --host",
		"serve": "remix-serve ./build/server/index.js",
		"api": "nodemon server.js",
		"indirect": "npm run dev",
		"many": "turbo run dev"
	}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"docker compose up":                     "compose",
		"pnpm dev":                              "vite",
		"npm run indirect":                      "vite",
		"npm run next":                          "next",
		"yarn astro":                            "astro",
		"pnpm nuxt":                             "nuxt",
		"pnpm remix":                            "remix",
		"pnpm rr":                               "react-router",
		"npm run serve":                         "remix-serve",
		"pnpm api":                              "node",
		"pnpm many":                             "",
		"next dev -H 0.0.0.0":                   "next",
		"go run ./cmd/web":                      "go",
		"air":                                   "go",
		"bin/rails server -b 0.0.0.0":           "rails",
		"bundle exec rails s":                   "rails",
		"mix phx.server":                        "phoenix",
		"iex -S mix phx.server":                 "phoenix",
		"uvicorn main:app --host 0.0.0.0":       "uvicorn",
		"uv run uvicorn main:app":               "uvicorn",
		"python3 -m uvicorn main:app":           "uvicorn",
		"fastapi run main.py":                   "fastapi",
		"uv run fastapi dev":                    "fastapi",
		"flask --app app run --host 0.0.0.0":    "flask",
		".venv/bin/flask run":                   "flask",
		"python manage.py runserver 0.0.0.0:80": "django",
		"poetry run python manage.py runserver": "django",
		"gunicorn app:app -b 0.0.0.0:8000":      "gunicorn",
		".venv/bin/gunicorn app:app":            "gunicorn",
		"hypercorn main:app --bind 0.0.0.0:80":  "hypercorn",
		"python3 -m http.server 8000":           "http.server",
		"python3 server.py":                     "",
		"cargo run":                             "",
		"make serve":                            "",
	}
	for cmd, want := range cases {
		if got := CommandKind(dir, cmd); got != want {
			t.Errorf("CommandKind(%q) = %q, want %q", cmd, got, want)
		}
	}
}
