package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// Temporary servers run a configured server's command in another checkout
// (a git worktree or clone of the same repository). They are kept in state
// only, never written to the config file, and named <origin>--<worktree>.

// temporary describes a temporary server; it is persisted in the state file.
type temporary struct {
	Name     string `json:"name"`
	Origin   string `json:"origin"`   // the configured server it copies
	Worktree string `json:"worktree"` // top directory of the checkout it runs in
	Rel      string `json:"rel"`      // the origin's directory relative to its checkout
	Port     int    `json:"port"`
}

// TemporarySeparator joins a configured server's name and a worktree label
// in a temporary server's name; it keeps the name a valid host label.
const TemporarySeparator = "--"

// PruneInterval is how often temporary servers are checked for a checkout
// that no longer exists.
const PruneInterval = 10 * time.Second

// checkout is a git working tree: its top directory and the git directory
// shared by every worktree of the repository.
type checkout struct{ top, common string }

func gitCheckout(git, dir string) (checkout, error) {
	cmd := exec.Command(git, "-C", dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	// Repository variables of the daemon's own environment must not
	// redirect the lookup.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") && !strings.HasPrefix(kv, "GIT_COMMON_DIR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return checkout{}, errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return checkout{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return checkout{}, fmt.Errorf("unexpected git output for %s", dir)
	}
	return checkout{top: realDir(lines[0]), common: realDir(lines[1])}, nil
}

// realDir resolves symlinks (on macOS /tmp is /private/tmp), so paths from
// git and from the config compare equal.
func realDir(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

var labelJunk = regexp.MustCompile(`[^a-z0-9]+`)

// worktreeLabel turns a checkout's folder name into a short host-label part.
func worktreeLabel(top string) string {
	l := labelJunk.ReplaceAllString(strings.ToLower(filepath.Base(top)), "-")
	l = strings.Trim(l, "-")
	if len(l) > 20 {
		l = strings.Trim(l[:20], "-")
	}
	if l == "" {
		l = "worktree"
	}
	return l
}

// temporaryName names origin's copy for the checkout at top. The result is a
// valid server name (and DNS label) that taken does not report.
func temporaryName(origin, top string, taken func(string) bool) string {
	label := worktreeLabel(top)
	join := func(suffix string) string {
		base := origin
		if over := len(base) + len(TemporarySeparator) + len(suffix) - 63; over > 0 {
			base = strings.TrimRight(base[:max(len(base)-over, 1)], "-")
		}
		return base + TemporarySeparator + suffix
	}
	name := join(label)
	for i := 2; taken(name) || config.ValidName(name) != nil; i++ {
		name = join(fmt.Sprintf("%s-%d", label, i))
	}
	return name
}

// temporaryConfig derives a temporary server's configuration from its origin.
func temporaryConfig(origin config.Server, t *temporary) config.Server {
	sv := origin
	sv.Name = t.Name
	sv.Dir = filepath.Join(t.Worktree, t.Rel)
	sv.DirRaw = sv.Dir
	sv.PortAuto = false
	if origin.Port > 0 {
		sv.Port = t.Port
	} else {
		sv.Port = 0
	}
	if origin.Icon != "" {
		if rel, err := filepath.Rel(origin.Dir, origin.Icon); err == nil && !strings.HasPrefix(rel, "..") {
			sv.Icon = filepath.Join(sv.Dir, rel)
		}
	}
	return sv
}

// ForCheckout returns the server that runs name in the checkout containing
// dir: name itself for its own checkout, otherwise a temporary copy, created
// on first use with a free port. dir must be a checkout of the repository
// name runs from.
func (s *Supervisor) ForCheckout(name, dir string) (string, error) {
	s.mu.Lock()
	sv, ok := s.cfg.Server(name)
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown server %q", name)
	}
	dir = paths.ExpandHome(dir)
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("dir must be an absolute path, got %q", dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("directory not found: %s", paths.ShortenHome(dir))
	}
	git := s.LookPath("git")
	if git == "" {
		git = "git"
	}
	want, err := gitCheckout(git, dir)
	if err != nil {
		if within(realDir(dir), realDir(sv.Dir)) {
			return name, nil
		}
		return "", fmt.Errorf("%s is not a git checkout, so it cannot run a copy of %s: %v", paths.ShortenHome(dir), name, err)
	}
	orig, err := gitCheckout(git, sv.Dir)
	if err != nil {
		return "", fmt.Errorf("%s does not run in a git checkout (%s), so it has no copies for other checkouts", name, paths.ShortenHome(sv.Dir))
	}
	if want.top == orig.top {
		return name, nil
	}
	if want.common != orig.common {
		return "", fmt.Errorf("%s is not a worktree of the repository %s runs from (%s); only its linked git worktrees can run a copy", paths.ShortenHome(want.top), name, paths.ShortenHome(orig.top))
	}
	rel, err := filepath.Rel(orig.top, realDir(sv.Dir))
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = "."
	}
	return s.addTemporary(sv, want.top, rel)
}

// addTemporary returns origin's temporary server for the checkout at top,
// creating it if there is none.
func (s *Supervisor) addTemporary(origin config.Server, top, rel string) (string, error) {
	s.tempMu.Lock()
	defer s.tempMu.Unlock()

	s.mu.Lock()
	for name, p := range s.procs {
		if p.temp != nil && p.temp.Origin == origin.Name && p.temp.Worktree == top {
			s.mu.Unlock()
			return name, nil
		}
	}
	if s.closing {
		s.mu.Unlock()
		return "", errors.New("the daemon is shutting down")
	}
	cfg := s.cfg
	used := s.usedPortsLocked()
	s.mu.Unlock()

	port := 0
	if origin.Port > 0 {
		// Probing runs without the lock; tempMu keeps other copies from
		// picking the same port meanwhile.
		port = cfg.FreePort(func(p int) bool { return used[p] || ports.Listening(p) })
		if port == 0 {
			return "", errors.New("no free port for a temporary server")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.cfg.Server(origin.Name); ok {
		origin = cur
	} else {
		return "", fmt.Errorf("unknown server %q", origin.Name)
	}
	t := &temporary{Origin: origin.Name, Worktree: top, Rel: rel, Port: port}
	t.Name = temporaryName(origin.Name, top, func(n string) bool {
		_, configured := s.cfg.Server(n)
		return configured || s.procs[n] != nil
	})
	s.addTemporaryLocked(origin, t)
	s.reorderLocked()
	s.saveLocked()
	s.changedLocked()
	return t.Name, nil
}

func (s *Supervisor) addTemporaryLocked(origin config.Server, t *temporary) {
	s.procs[t.Name] = &proc{
		cfg:    temporaryConfig(origin, t),
		temp:   t,
		status: Stopped,
		logs:   newLogBuffer(s.logDir, t.Name),
		cols:   defaultCols,
		rows:   defaultRows,
	}
}

// usedPortsLocked lists every port a server is configured with or runs on.
func (s *Supervisor) usedPortsLocked() map[int]bool {
	used := map[int]bool{}
	if s.cfg != nil {
		used[s.cfg.Port] = true
		for _, sv := range s.cfg.Servers {
			used[sv.Port] = true
		}
	}
	for _, p := range s.procs {
		used[p.cfg.Port] = true
		used[p.running.Port] = true
	}
	delete(used, 0)
	return used
}

// restoreTemporaryLocked recreates temporary servers saved by an earlier
// daemon whose configured server and checkout still exist.
func (s *Supervisor) restoreTemporaryLocked(saved []temporary) {
	for i := range saved {
		t := saved[i]
		origin, ok := s.cfg.Server(t.Origin)
		if !ok || t.Name == "" || s.procs[t.Name] != nil {
			continue
		}
		if _, err := os.Stat(t.Worktree); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if _, configured := s.cfg.Server(t.Name); configured {
			continue
		}
		s.addTemporaryLocked(origin, &t)
	}
	s.reorderLocked()
}

// updateTemporaryLocked follows a config reload: a temporary server takes
// its origin's new definition (keeping its checkout and port), and reports
// false when the origin is gone.
func (s *Supervisor) updateTemporaryLocked(p *proc) bool {
	origin, ok := s.cfg.Server(p.temp.Origin)
	if !ok {
		return false
	}
	if origin.Port > 0 && p.pid == 0 {
		taken := false
		for _, sv := range s.cfg.Servers {
			taken = taken || sv.Port == p.temp.Port
		}
		if taken || p.temp.Port == 0 || p.temp.Port == s.cfg.Port {
			used := s.usedPortsLocked()
			p.temp.Port = s.cfg.FreePort(func(n int) bool { return used[n] })
		}
	}
	p.cfg = temporaryConfig(origin, p.temp)
	return true
}

// reorderLocked lists servers in config order, each followed by its
// temporary copies, then removed servers still shutting down.
func (s *Supervisor) reorderLocked() {
	copies := map[string][]string{}
	var gone []string
	configured := map[string]bool{}
	for _, sv := range s.cfg.Servers {
		configured[sv.Name] = true
	}
	for name, p := range s.procs {
		switch {
		case configured[name]:
		case p.temp != nil && configured[p.temp.Origin] && !p.removed:
			copies[p.temp.Origin] = append(copies[p.temp.Origin], name)
		default:
			gone = append(gone, name)
		}
	}
	order := make([]string, 0, len(s.procs))
	for _, sv := range s.cfg.Servers {
		order = append(order, sv.Name)
		names := copies[sv.Name]
		slices.Sort(names)
		order = append(order, names...)
	}
	// Removed servers keep their earlier relative position at the end.
	slices.SortStableFunc(gone, func(a, b string) int {
		return slices.Index(s.order, a) - slices.Index(s.order, b)
	})
	s.order = append(order, gone...)
}

// Dismiss stops a server on request. A temporary server is removed as well,
// with its log files.
func (s *Supervisor) Dismiss(name string) error {
	if err := s.Stop(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.procs[name]; p != nil && p.temp != nil && p.pid == 0 {
		s.dropLocked(name, p)
		s.saveLocked()
		s.changedLocked()
	}
	return nil
}

// dropLocked forgets a stopped temporary server and deletes its logs.
func (s *Supervisor) dropLocked(name string, p *proc) {
	p.logs.Close()
	delete(s.procs, name)
	if i := slices.Index(s.order, name); i >= 0 {
		s.order = slices.Delete(s.order, i, i+1)
	}
	log := filepath.Join(s.logDir, name+".log")
	_ = os.Remove(log)
	_ = os.Remove(log + ".1")
}

// IsTemporary reports whether name is a temporary server.
func (s *Supervisor) IsTemporary(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.procs[name]
	return p != nil && p.temp != nil
}

// TemporaryPorts reports the ports temporary servers are assigned or run on,
// which automatic ports in the config must skip.
func (s *Supervisor) TemporaryPorts() func(port int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := map[int]bool{}
	for _, p := range s.procs {
		if p.temp != nil && !p.removed {
			used[p.temp.Port] = true
			if p.pid != 0 {
				used[p.running.Port] = true
			}
		}
	}
	return func(port int) bool { return used[port] }
}

// SavedTemporaryPorts is TemporaryPorts for the temporary servers saved in
// stateDir, for use before a supervisor exists.
func SavedTemporaryPorts(stateDir string) func(port int) bool {
	s := &Supervisor{state: statePath(stateDir)}
	used := map[int]bool{}
	for _, t := range s.load().Temporary {
		used[t.Port] = true
	}
	return func(port int) bool { return used[port] }
}

// Definitions returns the definitions of configured and temporary servers,
// each temporary server after its origin.
func (s *Supervisor) Definitions() []config.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]config.Server, 0, len(s.order))
	for _, name := range s.order {
		if p := s.procs[name]; p != nil && !p.removed {
			out = append(out, p.cfg)
		}
	}
	return out
}

// Lookup returns the definition of any server the supervisor knows,
// configured or temporary.
func (s *Supervisor) Lookup(name string) (config.Server, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.procs[name]; p != nil {
		return p.cfg, true
	}
	return config.Server{}, false
}

// pruneTemporary removes temporary servers whose checkout no longer exists.
func (s *Supervisor) pruneTemporary() {
	s.mu.Lock()
	var gone []string
	for name, p := range s.procs {
		if p.temp == nil {
			continue
		}
		if _, err := os.Stat(p.temp.Worktree); errors.Is(err, os.ErrNotExist) {
			gone = append(gone, name)
		}
	}
	s.mu.Unlock()
	for _, name := range gone {
		_ = s.Dismiss(name)
	}
}
