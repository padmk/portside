package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/config"
)

// ── desktop integration ──────────────────────────────────────────────────

type openBody struct {
	Target string `json:"target"` // "folder", "editor", "terminal", "config", "logs"
	Name   string `json:"name,omitempty"`
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	var body openBody
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.sup.Config()
	var target string
	switch body.Target {
	case "config":
		target = s.opt.ConfigPath
	case "logs":
		target = filepath.Join(s.opt.StateDir, "logs")
		if body.Name != "" {
			target = filepath.Join(target, body.Name+".log")
		}
	default:
		sv, ok := s.sup.Lookup(body.Name)
		if !ok {
			fail(w, http.StatusNotFound, errors.New("unknown server"))
			return
		}
		target = sv.Dir
	}
	var c *exec.Cmd
	var err error
	switch body.Target {
	case "folder", "logs":
		err = reveal(target)
	case "editor", "config":
		c, err = s.desktop().editorCommand(cfg.Editor, target)
	case "terminal":
		c, err = s.desktop().terminalCommand(cfg.Terminal, target)
	default:
		err = fmt.Errorf("unknown target %q", body.Target)
	}
	if err == nil && c != nil {
		c.Env = s.sup.Environ()
		if runtime.GOOS == "darwin" && filepath.Base(c.Path) == "open" {
			err = runOpen(c)
		} else {
			err = launch(c)
		}
		if err != nil {
			switch {
			case (body.Target == "editor" || body.Target == "config") && cfg.Editor != "":
				err = fmt.Errorf("editor setting %q: %w", cfg.Editor, err)
			case body.Target == "terminal" && cfg.Terminal != "":
				err = fmt.Errorf("terminal setting %q: %w", cfg.Terminal, err)
			}
		}
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// appOption is one choice for the editor or terminal setting.
type appOption struct {
	Value string `json:"value"` // written to the config
	Label string `json:"label"`
}

type appsView struct {
	Editor       string      `json:"editor"`   // as configured, "" for automatic
	Terminal     string      `json:"terminal"` // as configured, "" for automatic
	Editors      []appOption `json:"editors"`
	Terminals    []appOption `json:"terminals"`
	AutoEditor   string      `json:"autoEditor"` // what automatic opens, "" when unknown
	AutoTerminal string      `json:"autoTerminal"`
}

func (s *Server) appsView() appsView {
	cfg := s.sup.Config()
	d := s.desktop()
	v := appsView{Editor: cfg.Editor, Terminal: cfg.Terminal, Editors: d.options(editorApps), Terminals: d.options(terminalApps)}
	if a := d.autoEditor(); a != nil {
		v.AutoEditor = a.Name
	}
	if a := d.autoTerminal(); a != nil {
		v.AutoTerminal = a.Name
	}
	return v
}

func (s *Server) getApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.appsView())
}

// setApp writes the editor or terminal setting; an empty value means automatic.
func (s *Server) setApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	value := strings.TrimSpace(body.Value)
	cfg := s.sup.Config()
	var current string
	switch body.Key {
	case "editor":
		current = cfg.Editor
	case "terminal":
		current = cfg.Terminal
	default:
		fail(w, http.StatusBadRequest, fmt.Errorf("unknown setting %q", body.Key))
		return
	}
	if strings.ContainsAny(value, "\r\n") {
		fail(w, http.StatusBadRequest, errors.New("the value must be one line"))
		return
	}
	if value != current {
		s.editMu.Lock()
		err := config.SetTopLevel(s.opt.ConfigPath, body.Key, config.Quote(value))
		if err == nil {
			err = s.reload()
		}
		s.editMu.Unlock()
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.appsView())
}

// launch starts a desktop helper and reaps it in the background so finished
// launchers do not linger as zombies.
func launch(c *exec.Cmd) error {
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

// runOpen waits for macOS open, which returns once the app has been asked to
// open the target and exits non-zero when the app or target cannot be found.
// It reports that failure with open's message; an open still running after a
// few seconds counts as started and is reaped in the background.
func runOpen(c *exec.Cmd) error {
	var out strings.Builder
	c.Stdout, c.Stderr = &out, &out
	if err := c.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			if msg := strings.TrimSpace(out.String()); msg != "" {
				return errors.New(msg)
			}
			return err
		}
		return nil
	case <-time.After(5 * time.Second):
		return nil
	}
}

func reveal(p string) error {
	if runtime.GOOS == "darwin" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return launch(exec.Command("open", "-R", p))
		}
		return launch(exec.Command("open", p))
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		p = filepath.Dir(p)
	}
	return launch(exec.Command("xdg-open", p))
}

// desktopApp is an editor or terminal that can open a directory.
type desktopApp struct {
	Name   string   // shown in the dashboard; the config value on macOS
	Bundle string   // macOS bundle ID, "" when there is no macOS app
	Cmds   []string // commands on PATH; the first is the config value on Linux
	// mac builds the macOS command for the app at path; nil opens dir with it.
	mac func(path, dir string) []string
	// args are the Linux arguments that open dir; nil runs the command in dir.
	args func(dir string) []string
}

// Editors in the order automatic tries them.
var editorApps = []desktopApp{
	{Name: "Cursor", Bundle: "com.todesktop.230313mzl4w4u92", Cmds: []string{"cursor"}},
	{Name: "Visual Studio Code", Bundle: "com.microsoft.VSCode", Cmds: []string{"code"}},
	{Name: "Zed", Bundle: "dev.zed.Zed", Cmds: []string{"zed", "zeditor"}},
	{Name: "Windsurf", Bundle: "com.exafunction.windsurf", Cmds: []string{"windsurf"}},
	{Name: "Sublime Text", Bundle: "com.sublimetext.4", Cmds: []string{"subl"}},
	{Name: "Visual Studio Code - Insiders", Bundle: "com.microsoft.VSCodeInsiders", Cmds: []string{"code-insiders"}},
	{Name: "VSCodium", Bundle: "com.vscodium", Cmds: []string{"codium"}},
	{Name: "Nova", Bundle: "com.panic.Nova", Cmds: []string{"nova"}},
	{Name: "BBEdit", Bundle: "com.barebones.bbedit", Cmds: []string{"bbedit"}},
	{Name: "IntelliJ IDEA", Bundle: "com.jetbrains.intellij", Cmds: []string{"idea"}},
	{Name: "IntelliJ IDEA CE", Bundle: "com.jetbrains.intellij.ce"},
	{Name: "WebStorm", Bundle: "com.jetbrains.WebStorm", Cmds: []string{"webstorm"}},
	{Name: "PyCharm", Bundle: "com.jetbrains.pycharm", Cmds: []string{"pycharm"}},
	{Name: "PyCharm CE", Bundle: "com.jetbrains.pycharm.ce"},
	{Name: "GoLand", Bundle: "com.jetbrains.goland", Cmds: []string{"goland"}},
	{Name: "RustRover", Bundle: "com.jetbrains.rustrover", Cmds: []string{"rustrover"}},
	{Name: "PhpStorm", Bundle: "com.jetbrains.PhpStorm", Cmds: []string{"phpstorm"}},
	{Name: "Xcode", Bundle: "com.apple.dt.Xcode"},
}

// openNew starts a new instance of a macOS app with arguments, for apps
// that take the directory only as a command-line flag.
func openNew(args ...string) func(path, dir string) []string {
	return func(path, dir string) []string {
		out := []string{"open", "-n", "-a", path, "--args"}
		for _, a := range args {
			out = append(out, strings.ReplaceAll(a, "{DIR}", dir))
		}
		return out
	}
}

func flags(args ...string) func(dir string) []string {
	return func(dir string) []string {
		out := make([]string, len(args))
		for i, a := range args {
			out[i] = strings.ReplaceAll(a, "{DIR}", dir)
		}
		return out
	}
}

// Terminals in the order automatic tries them; Terminal is the macOS fallback.
var terminalApps = []desktopApp{
	{Name: "Ghostty", Bundle: "com.mitchellh.ghostty", Cmds: []string{"ghostty"}, args: flags("--working-directory={DIR}")},
	{Name: "iTerm2", Bundle: "com.googlecode.iterm2"},
	{Name: "WezTerm", Bundle: "com.github.wez.wezterm", Cmds: []string{"wezterm"}, mac: openNew("start", "--cwd", "{DIR}"), args: flags("start", "--cwd", "{DIR}")},
	{Name: "kitty", Bundle: "net.kovidgoyal.kitty", Cmds: []string{"kitty"}, mac: openNew("--directory", "{DIR}"), args: flags("--directory", "{DIR}")},
	{Name: "Alacritty", Bundle: "org.alacritty", Cmds: []string{"alacritty"}, mac: openNew("--working-directory", "{DIR}"), args: flags("--working-directory", "{DIR}")},
	{Name: "Warp", Bundle: "dev.warp.Warp-Stable", mac: func(_, dir string) []string {
		// Percent-encode the path with %20 for spaces: QueryEscape's "+"
		// would reach Warp as a literal plus.
		return []string{"open", "warp://action/new_window?path=" + strings.ReplaceAll(url.QueryEscape(dir), "+", "%20")}
	}},
	{Name: "foot", Cmds: []string{"foot"}, args: flags("--working-directory={DIR}")},
	{Name: "Ptyxis", Cmds: []string{"ptyxis"}, args: flags("--new-window", "--working-directory={DIR}")},
	{Name: "GNOME Terminal", Cmds: []string{"gnome-terminal"}, args: flags("--working-directory={DIR}")},
	{Name: "Console", Cmds: []string{"kgx"}, args: flags("--working-directory={DIR}")},
	{Name: "Konsole", Cmds: []string{"konsole"}, args: flags("--workdir", "{DIR}")},
	{Name: "Xfce Terminal", Cmds: []string{"xfce4-terminal"}, args: flags("--working-directory={DIR}")},
	{Name: "Terminal", Bundle: "com.apple.Terminal"},
}

// desktop resolves editors and terminals on one operating system.
type desktop struct {
	goos   string
	lookup func(name string) string // command on PATH, "" when missing
	getenv func(key string) string  // the login-shell environment
	apps   func() map[string]string // installed macOS apps: bundle ID to path
}

func (s *Server) desktop() desktop {
	return desktop{
		goos:   runtime.GOOS,
		lookup: s.sup.LookPath,
		getenv: func(k string) string { return envValue(s.sup.Environ(), k) },
		apps:   installedApps,
	}
}

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

// available reports how app runs here: the macOS app's path, or a command
// on PATH.
func (d desktop) available(a desktopApp) (appPath, cmd string) {
	if d.goos == "darwin" && a.Bundle != "" {
		if p := d.apps()[a.Bundle]; p != "" {
			return p, ""
		}
	}
	for _, c := range a.Cmds {
		if p := d.lookup(c); p != "" {
			return "", p
		}
	}
	return "", ""
}

func (d desktop) options(list []desktopApp) []appOption {
	out := []appOption{}
	for _, a := range list {
		appPath, cmd := d.available(a)
		switch {
		case appPath != "":
			out = append(out, appOption{Value: a.Name, Label: a.Name})
		case cmd != "" && (d.goos != "darwin" || a.Bundle == ""):
			out = append(out, appOption{Value: a.Cmds[0], Label: a.Name})
		case cmd != "":
			out = append(out, appOption{Value: a.Cmds[0], Label: a.Name + " (" + a.Cmds[0] + ")"})
		}
	}
	return out
}

// named finds the app a setting names by its app name or one of its commands.
func named(list []desktopApp, setting string) *desktopApp {
	for i, a := range list {
		if strings.EqualFold(a.Name, setting) {
			return &list[i]
		}
		for _, c := range a.Cmds {
			if c == setting {
				return &list[i]
			}
		}
	}
	return nil
}

// appCommand opens target with app: the macOS app, else its command.
func (d desktop) appCommand(a desktopApp, target string, terminal bool) *exec.Cmd {
	appPath, cmd := d.available(a)
	var c *exec.Cmd
	switch {
	case appPath != "" && a.mac != nil:
		argv := a.mac(appPath, target)
		c = exec.Command(argv[0], argv[1:]...)
	case appPath != "":
		c = exec.Command("open", "-a", appPath, target)
	case cmd == "":
		return nil
	case terminal && a.args != nil:
		c = exec.Command(cmd, a.args(target)...)
	case terminal:
		c = exec.Command(cmd)
	default:
		c = exec.Command(cmd, target)
	}
	if terminal && appPath == "" {
		c.Dir = target
	}
	return c
}

// guiEditor returns the known editor $VISUAL or $EDITOR names, ignoring
// terminal editors such as vim, which have no window to open in.
func (d desktop) guiEditor() *desktopApp {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		f := strings.Fields(d.getenv(k))
		if len(f) == 0 {
			continue
		}
		if a := named(editorApps, filepath.Base(f[0])); a != nil {
			if p, c := d.available(*a); p != "" || c != "" {
				return a
			}
		}
	}
	return nil
}

func (d desktop) autoEditor() *desktopApp {
	if a := d.guiEditor(); a != nil {
		return a
	}
	for i, a := range editorApps {
		if p, c := d.available(a); p != "" || c != "" {
			return &editorApps[i]
		}
	}
	return nil
}

// editorCommand opens target with the configured editor, else $VISUAL or
// $EDITOR when it names a GUI editor, else the first installed known editor,
// else the system default application.
func (d desktop) editorCommand(setting, target string) (*exec.Cmd, error) {
	if setting = strings.TrimSpace(setting); setting != "" {
		if a := named(editorApps, setting); a != nil {
			if c := d.appCommand(*a, target, false); c != nil {
				return c, nil
			}
		}
		parts := strings.Fields(setting)
		if p := d.lookup(parts[0]); p != "" {
			return exec.Command(p, append(parts[1:], target)...), nil
		}
		if d.goos == "darwin" {
			return exec.Command("open", "-a", setting, target), nil
		}
		return nil, fmt.Errorf("editor command %q not found", parts[0])
	}
	if a := d.autoEditor(); a != nil {
		return d.appCommand(*a, target, false), nil
	}
	if d.goos == "darwin" {
		if st, err := os.Stat(target); err == nil && !st.IsDir() {
			return exec.Command("open", "-t", target), nil
		}
		return exec.Command("open", target), nil
	}
	return exec.Command("xdg-open", target), nil
}

func (d desktop) autoTerminal() *desktopApp {
	for i, a := range terminalApps {
		if p, c := d.available(a); p != "" || c != "" {
			return &terminalApps[i]
		}
	}
	return nil
}

// terminalCommand opens a terminal in dir: the configured app or command,
// else on Linux $TERMINAL or x-terminal-emulator, else the first installed
// known terminal, with Terminal as the macOS fallback.
func (d desktop) terminalCommand(setting, dir string) (*exec.Cmd, error) {
	if setting = strings.TrimSpace(setting); setting != "" {
		if a := named(terminalApps, setting); a != nil {
			if c := d.appCommand(*a, dir, true); c != nil {
				return c, nil
			}
		}
		parts := strings.Fields(setting)
		p := d.lookup(parts[0])
		if d.goos == "darwin" && p == "" {
			return exec.Command("open", "-a", setting, dir), nil
		}
		if p == "" {
			return nil, fmt.Errorf("terminal command %q not found", parts[0])
		}
		args, placeholder := parts[1:], false
		for i, arg := range args {
			if strings.Contains(arg, "{DIR}") {
				placeholder = true
				args[i] = strings.ReplaceAll(arg, "{DIR}", dir)
			}
		}
		if !placeholder {
			args = append(args, dir)
		}
		c := exec.Command(p, args...)
		c.Dir = dir
		return c, nil
	}
	if d.goos != "darwin" {
		if t := strings.Fields(d.getenv("TERMINAL")); len(t) > 0 {
			if a := named(terminalApps, filepath.Base(t[0])); a != nil && len(t) == 1 {
				if c := d.appCommand(*a, dir, true); c != nil {
					return c, nil
				}
			}
			if p := d.lookup(t[0]); p != "" {
				c := exec.Command(p, t[1:]...)
				c.Dir = dir
				return c, nil
			}
		}
		if p := d.lookup("x-terminal-emulator"); p != "" {
			c := exec.Command(p)
			c.Dir = dir
			return c, nil
		}
	}
	if a := d.autoTerminal(); a != nil {
		return d.appCommand(*a, dir, true), nil
	}
	if d.goos == "darwin" {
		return exec.Command("open", "-a", "Terminal", dir), nil
	}
	return nil, errors.New("no terminal emulator found")
}

// installedApps asks Launch Services where the known editor and terminal
// apps are, and remembers the answer for a minute.
func installedApps() map[string]string {
	appsCache.Lock()
	defer appsCache.Unlock()
	if appsCache.found != nil && time.Since(appsCache.at) < time.Minute {
		return appsCache.found
	}
	var ids []string
	for _, list := range [][]desktopApp{editorApps, terminalApps} {
		for _, a := range list {
			if a.Bundle != "" {
				ids = append(ids, a.Bundle)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", append([]string{"-l", "JavaScript", "-e", findAppsScript}, ids...)...).Output()
	found := map[string]string{}
	if err == nil {
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		for i, id := range ids {
			if i < len(lines) && strings.HasSuffix(lines[i], ".app") {
				found[id] = lines[i]
			}
		}
	}
	appsCache.found, appsCache.at = found, time.Now()
	return found
}

var appsCache struct {
	sync.Mutex
	at    time.Time
	found map[string]string
}

// findAppsScript prints one line per bundle ID argument: the app's path, or
// an empty line when it is not installed.
const findAppsScript = `ObjC.import("AppKit")
function run(argv) {
  const ws = $.NSWorkspace.sharedWorkspace
  return argv.map(id => {
    const u = ws.URLForApplicationWithBundleIdentifier(id)
    return u.isNil() ? "" : u.path.js
  }).join("\n")
}`
