package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/discover"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// ensureConfig creates the starter config when it does not exist yet.
func (e *env) ensureConfig() error {
	if _, err := os.Stat(e.cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := config.WriteStarter(e.cfgPath); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "Created %s\n", paths.ShortenHome(e.cfgPath))
	}
	return nil
}

func cmdInit(e *env, args []string) error {
	if _, err := os.Stat(e.cfgPath); err == nil {
		fmt.Fprintf(e.out, "%s already exists.\n", paths.ShortenHome(e.cfgPath))
		return nil
	}
	if err := e.ensureConfig(); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "\nNext steps:\n  %s scan --add      find projects and add them\n  %s open            open the dashboard\n", app.ID, app.ID)
	if _, err := os.Stat(paths.ExpandHome("~/dev/devctl/servers.conf")); err == nil {
		fmt.Fprintf(e.out, "  %s import devctl   bring over your devctl servers\n", app.ID)
	}
	return nil
}

// relDir writes dir relative to root when it is inside it.
func relDir(root, dir string) string {
	abs := paths.ExpandHome(dir)
	if !filepath.IsAbs(abs) {
		if wd, err := os.Getwd(); err == nil {
			abs = filepath.Join(wd, abs)
		}
	}
	if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return ""
		}
		return rel
	}
	return paths.ShortenHome(abs)
}

func cmdAdd(e *env, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	dir := fs.String("dir", ".", "")
	cmd := fs.String("cmd", "", "")
	port := fs.Int("port", -1, "")
	group := fs.String("group", "", "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) != 1 || strings.TrimSpace(*cmd) == "" {
		return usageError{"a name and --cmd are required"}
	}
	if err := e.ensureConfig(); err != nil {
		return err
	}
	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		return err
	}
	p := *port
	if p < 0 {
		p = cfg.FreePort(ports.Listening)
	}
	spec := config.Spec{Name: names[0], Dir: relDir(cfg.Root, *dir), Cmd: *cmd, Port: &p, Group: *group}
	if err := config.AddServer(e.cfgPath, spec); err != nil {
		return err
	}
	if p > 0 {
		fmt.Fprintf(e.out, "Added %s on port %d. Start it with `%s up %s`.\n", spec.Name, p, app.ID, spec.Name)
	} else {
		fmt.Fprintf(e.out, "Added %s. Start it with `%s up %s`.\n", spec.Name, app.ID, spec.Name)
	}
	return nil
}

func cmdRemove(e *env, args []string) error {
	if len(args) != 1 {
		return usageError{"rm takes exactly one server name"}
	}
	if c, ok := e.clientIfRunning(); ok {
		_ = c.Do(e.ctx, http.MethodPost, "/api/servers/"+args[0]+"/stop", nil, nil)
	}
	if err := config.RemoveServer(e.cfgPath, args[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "Removed %s.\n", args[0])
	return nil
}

func cmdEnable(on bool) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) != 1 {
			return usageError{"takes exactly one server name"}
		}
		if err := config.SetEnabled(e.cfgPath, args[0], on); err != nil {
			return err
		}
		word := "disabled"
		if on {
			word = "enabled"
		}
		fmt.Fprintf(e.out, "%s %s.\n", args[0], word)
		return nil
	}
}

func cmdScan(e *env, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	add := fs.Bool("add", false, "")
	asJSON := fs.Bool("json", false, "")
	depth := fs.Int("depth", 3, "")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := e.ensureConfig(); err != nil {
		return err
	}
	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		return err
	}
	root := cfg.Root
	if len(rest) > 0 {
		root = paths.ExpandHome(rest[0])
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	found := discover.Prepare(cfg, discover.Scan(root, *depth, nil))
	if *asJSON {
		return printJSON(e, found)
	}
	if len(found) == 0 {
		fmt.Fprintf(e.out, "No new projects found under %s.\n", paths.ShortenHome(root))
		return nil
	}
	for _, s := range found {
		port := "  -  "
		if s.Port > 0 {
			port = fmt.Sprintf(":%-5d", s.Port)
		}
		note := ""
		if s.DirectoryConfigured {
			note = " (folder already has servers)"
		}
		fmt.Fprintf(e.out, "  %-22s %s %-8s %-34s %s%s\n", s.Name, port, s.Kind, truncate(paths.ShortenHome(s.Dir), 34), e.paint(dim, s.Cmd), note)
	}
	if !*add {
		fmt.Fprintf(e.out, "\nAdd them all with `%s scan --add`, or pick in the dashboard.\n", app.ID)
		return nil
	}
	added, failed := 0, 0
	for _, s := range found {
		p := s.Port
		spec := config.Spec{Name: s.Name, Dir: relDir(cfg.Root, s.Dir), Cmd: s.Cmd, Port: &p, Stop: s.StopAfter}
		if err := config.AddServer(e.cfgPath, spec); err != nil {
			failed++
			fmt.Fprintf(e.errOut, "%s %s: %v\n", e.paint(red, "✗"), s.Name, err)
			continue
		}
		added++
	}
	fmt.Fprintf(e.out, "\nAdded %d servers to %s.\n", added, paths.ShortenHome(e.cfgPath))
	if failed > 0 {
		return silentError{1}
	}
	return nil
}

func cmdImport(e *env, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 || rest[0] != "devctl" {
		return usageError{"only `import devctl` is supported"}
	}
	src := paths.ExpandHome("~/dev/devctl/servers.conf")
	if len(rest) > 1 {
		src = paths.ExpandHome(rest[1])
	}
	imp, err := config.ImportDevctl(src)
	if err != nil {
		return err
	}
	if *dry {
		fmt.Fprintf(e.out, "root = %s\n", config.Quote(imp.Root))
		if imp.PathDir != "" {
			fmt.Fprintf(e.out, "path = [%s]\n", config.Quote(imp.PathDir))
		}
		for _, s := range imp.Servers {
			fmt.Fprintf(e.out, "\n%s", config.Render(s))
		}
		for _, s := range imp.Skipped {
			fmt.Fprintf(e.errOut, "skipped %s\n", s)
		}
		return nil
	}
	if err := e.ensureConfig(); err != nil {
		return err
	}
	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		return err
	}
	if len(cfg.Servers) == 0 {
		if err := config.SetTopLevel(e.cfgPath, "root", config.Quote(imp.Root)); err != nil {
			return err
		}
		if cfg, err = config.Load(e.cfgPath); err != nil {
			return err
		}
	}
	// devctl directories are relative to devctl's root; write them relative to ours.
	importRoot := paths.ExpandHome(imp.Root)
	for i := range imp.Servers {
		dir := paths.ExpandHome(imp.Servers[i].Dir)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(importRoot, dir)
		}
		imp.Servers[i].Dir = relDir(cfg.Root, dir)
	}
	if imp.PathDir != "" && len(cfg.PathPrepend) == 0 {
		if err := config.SetTopLevel(e.cfgPath, "path", "["+config.Quote(imp.PathDir)+"]"); err != nil {
			return err
		}
	}
	added := 0
	for _, s := range imp.Servers {
		if _, exists := cfg.Server(s.Name); exists {
			fmt.Fprintf(e.out, "  %s %s already configured\n", e.paint(dim, "•"), s.Name)
			continue
		}
		if err := config.AddServer(e.cfgPath, s); err != nil {
			fmt.Fprintf(e.errOut, "  %s %s: %v\n", e.paint(red, "✗"), s.Name, err)
			continue
		}
		added++
		fmt.Fprintf(e.out, "  %s %-22s :%d\n", e.paint(green, "+"), s.Name, *s.Port)
	}
	for _, s := range imp.Skipped {
		fmt.Fprintf(e.out, "  %s skipped %s\n", e.paint(dim, "•"), s)
	}
	fmt.Fprintf(e.out, "\nImported %d servers into %s. devctl itself was not changed.\n", added, paths.ShortenHome(e.cfgPath))
	return nil
}

func cmdConfig(e *env, args []string) error {
	sub := "path"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "path":
		fmt.Fprintln(e.out, e.cfgPath)
	case "edit":
		if err := e.ensureConfig(); err != nil {
			return err
		}
		ed := os.Getenv("VISUAL")
		if ed == "" {
			ed = os.Getenv("EDITOR")
		}
		if ed == "" {
			ed = "vi"
		}
		parts := strings.Fields(ed)
		c := exec.Command(parts[0], append(parts[1:], e.cfgPath)...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	case "check":
		cfg, err := config.Load(e.cfgPath)
		if err != nil {
			return err
		}
		for _, w := range cfg.Warnings {
			fmt.Fprintf(e.out, "%s %s\n", e.paint(yellow, "warning:"), w)
		}
		fmt.Fprintf(e.out, "%s %d servers, config is valid\n", e.paint(green, "✓"), len(cfg.Servers))
	default:
		return usageError{"unknown subcommand " + sub}
	}
	return nil
}
