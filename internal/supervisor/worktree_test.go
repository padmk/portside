package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/config"
)

func TestTemporaryName(t *testing.T) {
	none := func(string) bool { return false }
	if got := temporaryName("web", "/x/.claude/worktrees/Agent_A46c", none); got != "web--agent-a46c" {
		t.Errorf("name = %q", got)
	}
	if got := temporaryName("web", "/x/.t3/worktrees/proj/0123456789abcdef0123456789", none); got != "web--0123456789abcdef0123" {
		t.Errorf("long label = %q", got)
	}
	taken := func(n string) bool { return n == "web--feature" }
	if got := temporaryName("web", "/x/feature", taken); got != "web--feature-2" {
		t.Errorf("collision = %q", got)
	}
	long := strings.Repeat("a", 60)
	got := temporaryName(long, "/x/feature", none)
	if config.ValidName(got) != nil || len(got) > 63 || !strings.HasSuffix(got, "--feature") {
		t.Errorf("long origin = %q", got)
	}
	if got := temporaryName("web", "/x/___", none); got != "web--worktree" {
		t.Errorf("empty label = %q", got)
	}
}

// repoWithWorktree creates a repository with a commit and a linked worktree
// and returns both top directories (symlinks resolved).
func repoWithWorktree(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := realDir(t.TempDir())
	main := filepath.Join(base, "main")
	wt := filepath.Join(base, "feature-x")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(main, "apps", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(base, "init", "-q", main)
	if err := os.WriteFile(filepath.Join(main, "apps", "web", "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "add", ".")
	run(main, "commit", "-q", "-m", "init")
	run(main, "worktree", "add", "-q", wt)
	return main, wt
}

func worktreeConfig(main string) *config.Config {
	return &config.Config{Port: 7878, Servers: []config.Server{
		{Name: "db", Dir: main, Cmd: "true", Port: 5432, Enabled: true},
		{Name: "web", Dir: filepath.Join(main, "apps", "web"), Cmd: "python3 -m http.server {PORT}", Port: 3001,
			DependsOn: []string{"db"}, Restart: config.RestartOnFailure, Health: "/", Enabled: true},
	}}
}

func TestForCheckoutCreatesTemporaryCopy(t *testing.T) {
	main, wt := repoWithWorktree(t)
	cfg := worktreeConfig(main)
	state := t.TempDir()
	s := New(cfg, state)

	// The configured server's own checkout, also from a subdirectory.
	for _, dir := range []string{main, filepath.Join(main, "apps")} {
		if got, err := s.ForCheckout("web", dir); err != nil || got != "web" {
			t.Fatalf("own checkout %s: %q, %v", dir, got, err)
		}
	}

	name, err := s.ForCheckout("web", filepath.Join(wt, "apps"))
	if err != nil {
		t.Fatal(err)
	}
	if name != "web--feature-x" {
		t.Fatalf("name = %q", name)
	}
	if again, err := s.ForCheckout("web", wt); err != nil || again != name {
		t.Fatalf("second call: %q, %v", again, err)
	}
	sv, ok := s.Lookup(name)
	if !ok {
		t.Fatal("temporary server not found")
	}
	if want := filepath.Join(wt, "apps", "web"); sv.Dir != want {
		t.Errorf("dir = %s, want %s", sv.Dir, want)
	}
	if sv.Port == 0 || sv.Port == 3001 || sv.Port == 5432 || sv.Port == cfg.Port {
		t.Errorf("port = %d", sv.Port)
	}
	if sv.Cmd != cfg.Servers[1].Cmd || sv.Health != "/" || sv.Restart != config.RestartOnFailure || len(sv.DependsOn) != 1 || sv.DependsOn[0] != "db" {
		t.Errorf("definition not copied: %+v", sv)
	}

	var view *View
	order := []string{}
	for _, v := range s.Snapshot(7878) {
		order = append(order, v.Name)
		if v.Name == name {
			view = &v
		}
	}
	if view == nil || !view.Temporary || view.Origin != "web" || view.Worktree != wt {
		t.Fatalf("view = %+v", view)
	}
	if strings.Join(order, ",") != "db,web,"+name {
		t.Errorf("order = %v", order)
	}

	// A second copy for another worktree gets another port.
	other := filepath.Join(filepath.Dir(wt), "other")
	cmd := exec.Command("git", "-C", main, "worktree", "add", "-q", other)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	second, err := s.ForCheckout("web", other)
	if err != nil {
		t.Fatal(err)
	}
	if sv2, _ := s.Lookup(second); sv2.Port == sv.Port || sv2.Port == 3001 {
		t.Errorf("second port %d collides", sv2.Port)
	}
}

func TestForCheckoutRejectsOtherRepository(t *testing.T) {
	main, _ := repoWithWorktree(t)
	_, unrelated := repoWithWorktree(t)
	s := New(worktreeConfig(main), t.TempDir())
	if _, err := s.ForCheckout("web", unrelated); err == nil || !strings.Contains(err.Error(), "not a worktree of the repository") {
		t.Errorf("unrelated checkout: %v", err)
	}
	if _, err := s.ForCheckout("web", t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a git checkout") {
		t.Errorf("plain directory: %v", err)
	}
	if _, err := s.ForCheckout("nope", main); err == nil {
		t.Error("unknown server accepted")
	}
}

func TestTemporaryServersPersistAndGo(t *testing.T) {
	main, wt := repoWithWorktree(t)
	cfg := worktreeConfig(main)
	state := t.TempDir()
	s := New(cfg, state)
	name, err := s.ForCheckout("web", wt)
	if err != nil {
		t.Fatal(err)
	}
	port := func(s *Supervisor) int { sv, _ := s.Lookup(name); return sv.Port }
	before := port(s)

	// A new daemon on the same state directory restores it with its port.
	s2 := New(cfg, state)
	if !s2.IsTemporary(name) || port(s2) != before {
		t.Fatalf("not restored: temporary=%v port=%d want %d", s2.IsTemporary(name), port(s2), before)
	}

	// A config reload keeps it and applies the origin's new command.
	edited := worktreeConfig(main)
	edited.Servers[1].Cmd = "python3 -m http.server {PORT} --bind 127.0.0.1"
	s2.Apply(edited)
	if sv, ok := s2.Lookup(name); !ok || sv.Cmd != edited.Servers[1].Cmd {
		t.Fatalf("after reload: %+v %v", sv, ok)
	}

	// Stopping it on request removes it.
	if err := s2.Dismiss(name); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Lookup(name); ok {
		t.Error("still present after Dismiss")
	}
	if s3 := New(cfg, state); s3.IsTemporary(name) {
		t.Error("restored after Dismiss")
	}

	// It goes when its origin leaves the config.
	name, _ = s2.ForCheckout("web", wt)
	s2.Apply(&config.Config{Port: 7878, Servers: cfg.Servers[:1]})
	if _, ok := s2.Lookup(name); ok {
		t.Error("kept without its origin")
	}

	// And when its checkout is deleted.
	s2.Apply(cfg)
	name, _ = s2.ForCheckout("web", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	s2.pruneTemporary()
	if _, ok := s2.Lookup(name); ok {
		t.Error("kept after its checkout was deleted")
	}
}
