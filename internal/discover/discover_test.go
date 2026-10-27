package discover

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/config"
)

func TestPrepareSharedDirectoryAndPorts(t *testing.T) {
	cfg := &config.Config{Port: 3002, Servers: []config.Server{
		{Name: "web", Dir: "/projects/app", Cmd: "npm run dev", Port: 3001},
	}}
	found := []Suggestion{
		{Name: "web", Dir: "/projects/app", Cmd: "npm run dev -- --host 127.0.0.1 --port {PORT} --strictPort"},
		{Name: "web", Dir: "/projects/app", Cmd: "docker compose up --menu=false", NoPort: true},
		{Name: "api", Dir: "/projects/api", Cmd: "uv run uvicorn main:app --port {PORT}"},
		{Name: "pinned", Dir: "/projects/pinned", Cmd: "run --port 3003", Port: 3003},
		{Name: "duplicate", Dir: "/projects/api", Cmd: "uv run uvicorn main:app --port {PORT}"},
	}
	got := prepare(cfg, found, func(p int) bool { return p == 3004 })
	if len(got) != 3 {
		t.Fatalf("suggestions = %+v", got)
	}
	if got[0].Name != "web-2" || !got[0].DirectoryConfigured || got[0].Port != 0 {
		t.Errorf("shared directory = %+v", got[0])
	}
	if got[1].Port != 3005 || got[1].DirectoryConfigured || got[2].Port != 3003 {
		t.Errorf("ports = %+v", got)
	}
}

func TestPrepareDistinguishesComposeProjects(t *testing.T) {
	cfg := &config.Config{Port: 7878, Servers: []config.Server{
		{Name: "services", Dir: "/app", Cmd: "docker compose -p first up", Port: 0},
	}}
	got := prepare(cfg, []Suggestion{
		{Name: "first", Dir: "/app", Cmd: "docker compose -p first up --menu=false", NoPort: true},
		{Name: "second", Dir: "/app", Cmd: "docker compose -p second up --menu=false", NoPort: true},
	}, func(int) bool { return false })
	if len(got) != 1 || got[0].Name != "second" {
		t.Fatalf("suggestions = %+v", got)
	}
}

func TestDetectPython(t *testing.T) {
	has := func(name string) bool { return name == "uv" }
	uv := map[string]string{"uv.lock": ""}
	with := func(base map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		maps.Copy(m, base)
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"fastapi", with(uv, "pyproject.toml", "[project]\ndependencies = ['fastapi[standard]>=0.100']\n", "main.py", "from fastapi import FastAPI\napp = FastAPI()\n"), []string{"uv run uvicorn main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"uvicorn", with(uv, "pyproject.toml", "[project]\ndependencies = ['uvicorn']\n", "api/main.py", "from starlette.applications import Starlette\napp = Starlette()\n"), []string{"uv run uvicorn api.main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"scripts", with(uv, "pyproject.toml", "[project.scripts]\nworker = 'tasks:main'\n'bad;script' = 'tasks:main'\n"), []string{"uv run worker"}},
		{"scripts without environment", with(nil, "pyproject.toml", "[project.scripts]\nworker = 'tasks:main'\n"), nil},
		{"no app", with(uv, "pyproject.toml", "[project]\ndependencies = ['fastapi']\n", "main.py", "def create_app():\n    return FastAPI()\n"), nil},
		{"unrelated", with(uv, "pyproject.toml", "[project]\ndependencies = ['requests']\n", "main.py", "app = FastAPI()\n"), nil},
		{"poetry", with(nil, "poetry.lock", "", "pyproject.toml", "[tool.poetry.dependencies]\npython = '^3.12'\nfastapi = '*'\n", "main.py", "app = FastAPI()\n"), []string{"poetry run uvicorn main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"requirements", with(nil, "requirements.txt", "# web\nuvicorn[standard]==0.30\n", "app.py", "app = FastAPI()\n"), []string{"python3 -m uvicorn app:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"flask venv", with(nil, "requirements.txt", "Flask>=3\n", ".venv/bin/python", "", "wsgi.py", "from flask import Flask\napp = Flask(__name__)\n"), []string{".venv/bin/python -m flask --app wsgi run -h 127.0.0.1 -p {PORT}"}},
		{"uv installed", with(nil, "pyproject.toml", "[project]\nname = 'api'\ndependencies = ['fastapi']\n", "main.py", "app = FastAPI()\n"), []string{"uv run uvicorn main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"uv venv", with(nil, "pyproject.toml", "[project]\nname = 'api'\ndependencies = ['fastapi']\n", ".venv/bin/python", "", ".venv/pyvenv.cfg", "home = /usr/bin\nuv = 0.5.0\n", "main.py", "app = FastAPI()\n"), []string{"uv run uvicorn main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"pip venv", with(nil, "pyproject.toml", "[project]\nname = 'api'\ndependencies = ['fastapi']\n", ".venv/bin/python", "", ".venv/pyvenv.cfg", "home = /usr/bin\n", "main.py", "app = FastAPI()\n"), []string{".venv/bin/python -m uvicorn main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"pip venv with tool.uv", with(nil, "pyproject.toml", "[project]\nname = 'api'\ndependencies = ['fastapi']\n\n[tool.uv]\ndev-dependencies = []\n", ".venv/bin/python", "", "main.py", "app = FastAPI()\n"), []string{"uv run uvicorn main:app --reload --host 127.0.0.1 --port {PORT}"}},
		{"flask factory", with(nil, "Pipfile", "[packages]\nflask = '*'\n", "app/__init__.py", "def create_app():\n    pass\n"), []string{"pipenv run flask --app app run -h 127.0.0.1 -p {PORT}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := detectPython(dir, "api", has)
			if len(got) != len(tc.want) {
				t.Fatalf("suggestions = %+v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i].Cmd != want || (strings.HasPrefix(want, "uv run worker") && !got[i].NoPort) {
					t.Errorf("suggestion = %+v", got[i])
				}
			}
		})
	}
}
