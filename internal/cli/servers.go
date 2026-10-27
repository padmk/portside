package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

func (e *env) state() (*server.State, error) {
	c, err := e.client()
	if err != nil {
		return nil, err
	}
	return e.stateFrom(c)
}

// stateFrom reads the state from the daemon c talks to.
func (e *env) stateFrom(c *client.Client) (*server.State, error) {
	var st server.State
	if err := c.Do(e.ctx, http.MethodGet, "/api/state", nil, &st); err != nil {
		return nil, err
	}
	for i := range st.Servers {
		e.localize(&st.Servers[i])
	}
	return &st, nil
}

func cmdStatus(e *env, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(e, st.Servers)
	}
	if e.conn != nil {
		fmt.Fprintf(e.out, "%s\n", e.paint(dim, "on "+e.conn.Peer.Name))
	}
	if e.conn == nil && app.Older(st.Daemon.Version) {
		fmt.Fprintf(e.errOut, "note: the daemon runs %s, older than this command (%s); `%s daemon restart` updates it\n\n", st.Daemon.Version, app.Version, app.ID)
	}
	if st.Config.Error != "" {
		fmt.Fprintf(e.errOut, "%s %s\n\n", e.paint(red, "config error:"), st.Config.Error)
	}
	if len(st.Servers) == 0 && e.conn != nil {
		fmt.Fprintln(e.out, "No servers.")
		return nil
	}
	if len(st.Servers) == 0 {
		fmt.Fprintf(e.out, "No servers yet. Add one with `%s add`, find some with `%s scan`,\nor open the dashboard with `%s open`.\n", app.ID, app.ID, app.ID)
		if st.Remote != nil {
			fmt.Fprintf(e.out, "Connected to %s: `%s --on %s status` lists its servers.\n", st.Remote.Peer.Name, app.ID, st.Remote.Peer.Label)
		}
		return nil
	}
	nameW := 4
	for _, s := range st.Servers {
		if len(s.Name) > nameW {
			nameW = len(s.Name)
		}
	}
	// Same order as the dashboard: groups in order of appearance, ungrouped last.
	ordered := make([]supervisor.View, 0, len(st.Servers))
	for _, g := range st.Groups {
		for _, s := range st.Servers {
			if s.Group == g {
				ordered = append(ordered, s)
			}
		}
	}
	for _, s := range st.Servers {
		if s.Group == "" {
			ordered = append(ordered, s)
		}
	}
	exposed := 0
	group := "\x00"
	for _, s := range ordered {
		if s.Group != group && len(st.Groups) > 0 {
			group = s.Group
			label := s.Group
			if label == "" {
				label = "ungrouped"
			}
			fmt.Fprintf(e.out, "%s\n", e.paint(dim, strings.ToUpper(plain(label))))
		}
		port := "  -  "
		if s.Port > 0 {
			port = fmt.Sprintf(":%-5d", s.Port)
		}
		usage := ""
		if s.PID != 0 {
			usage = fmt.Sprintf("%5.1f%%  %7s", s.CPU, humanBytes(s.Mem))
		}
		note := s.Note
		if s.External != nil && s.PID == 0 {
			note = fmt.Sprintf("port held by %s (pid %d)", s.External.Command, s.External.PID)
		} else if s.ForwardedFrom != "" {
			note = "port forwarded from " + s.ForwardedFrom
		}
		if s.Stale {
			note = strings.TrimSpace(note + " config changed, restart to apply")
		}
		if s.Temporary {
			note = strings.TrimSpace("temporary in " + paths.ShortenHome(s.Worktree) + " " + note)
		}
		note = plain(note) // before the color sequences go in
		if s.Unready {
			note = e.paint(yellow, note)
		} else {
			note = e.paint(dim, note)
		}
		if s.NetworkWarning {
			exposed++
			note = strings.TrimSpace(e.paint(yellow, "may be reachable from the network") + " " + note)
		}
		fmt.Fprintf(e.out, "  %s %-*s  %-9s %s  %-16s %s\n",
			e.statusDot(s), nameW, plain(s.Name), e.statusWord(s), port, usage, note)
	}
	if exposed > 0 {
		fmt.Fprintf(e.out, "\n%s\n", e.paint(dim, "May be reachable from the network: unless a firewall blocks it, other devices on the network\ncan connect. Bind the server to 127.0.0.1, or set lan = true in its config if that is intended."))
	}
	if e.conn == nil {
		fmt.Fprintf(e.out, "\n%s %s\n", e.paint(dim, "dashboard"), client.DashboardURL(st.Daemon.Port))
	}
	return nil
}

func (e *env) statusDot(s supervisor.View) string {
	switch {
	case s.Status == supervisor.Running && s.Unready:
		return e.paint(yellow, "●")
	case s.Status == supervisor.Running:
		return e.paint(green, "●")
	case s.Status == supervisor.Starting || s.Status == supervisor.Stopping:
		return e.paint(yellow, "◐")
	case s.Status == supervisor.Crashed || s.Status == supervisor.Blocked:
		return e.paint(red, "●")
	}
	if s.External != nil {
		return e.paint(blue, "○")
	}
	return e.paint(dim, "○")
}

func (e *env) statusWord(s supervisor.View) string {
	w := string(s.Status)
	if s.Status == supervisor.Stopped && s.External != nil {
		w = "external"
	}
	return fmt.Sprintf("%-9s", w)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n > 0:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return ""
}

func printJSON(e *env, v any) error {
	enc := json.NewEncoder(e.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type bulkResp struct {
	Results []struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	} `json:"results"`
}

func bulk(e *env, action string, args []string, allowWait bool) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	all := fs.Bool("all", false, "")
	wait := fs.Bool("wait", false, "")
	takeover := fs.Bool("takeover", false, "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) == 0 && !*all {
		return usageError{"name a server or group, or pass --all"}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var res bulkResp
	if err := c.Do(e.ctx, http.MethodPost, "/api/bulk", map[string]any{"action": action, "names": names, "all": *all, "takeover": *takeover}, &res); err != nil {
		return err
	}
	failed := false
	for _, r := range res.Results {
		if r.Error != "" {
			failed = true
			fmt.Fprintf(e.errOut, "%s %s: %s\n", e.paint(red, "✗"), r.Name, r.Error)
		}
	}
	if failed && !(allowWait && *wait) {
		return silentError{1}
	}
	if allowWait && *wait {
		return waitAndReport(e, c, names, *all)
	}
	return cmdStatus(e, nil)
}

func waitAndReport(e *env, c *client.Client, names []string, all bool) error {
	st, err := e.state()
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	failed := false
	for _, s := range st.Servers {
		if !all && !want[s.Name] && !want[s.Group] {
			continue
		}
		if all && !s.Enabled {
			continue
		}
		var v supervisor.View
		err := c.Do(e.ctx, http.MethodPost, "/api/servers/"+url.PathEscape(s.Name)+"/ensure", map[string]string{"timeout": "3m"}, &v)
		if err != nil {
			failed = true
			fmt.Fprintf(e.errOut, "%s %s: %v\n", e.paint(red, "✗"), s.Name, err)
			continue
		}
		e.localize(&v)
		fmt.Fprintf(e.out, "%s %-20s %s\n", e.paint(green, "●"), v.Name, v.URL)
	}
	if failed {
		return silentError{1}
	}
	return nil
}

func cmdUp(e *env, args []string) error      { return bulk(e, "start", args, true) }
func cmdDown(e *env, args []string) error    { return bulk(e, "stop", args, false) }
func cmdRestart(e *env, args []string) error { return bulk(e, "restart", args, true) }

func cmdEnsure(e *env, args []string) error {
	fs := flag.NewFlagSet("ensure", flag.ContinueOnError)
	timeout := fs.String("timeout", supervisor.ReadyTimeout.String(), "")
	asJSON := fs.Bool("json", false, "")
	takeover := fs.Bool("takeover", false, "")
	dir := fs.String("dir", "", "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return usageError{"ensure takes exactly one server name"}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	body := map[string]any{"timeout": *timeout, "takeover": *takeover}
	if *dir != "" {
		// A relative directory means one on this computer.
		if e.conn == nil {
			if abs, err := filepath.Abs(*dir); err == nil {
				*dir = abs
			}
		}
		body["dir"] = *dir
	}
	var v supervisor.View
	err = c.Do(e.ctx, http.MethodPost, "/api/servers/"+url.PathEscape(names[0])+"/ensure", body, &v)
	if err != nil {
		var apiErr *client.APIError
		if *asJSON && errors.As(err, &apiErr) && len(apiErr.Body) > 0 {
			_, _ = e.out.Write(apiErr.Body)
			return silentError{1}
		}
		return err
	}
	e.localize(&v)
	if *asJSON {
		return printJSON(e, v)
	}
	if v.URL != "" {
		fmt.Fprintln(e.out, v.URL)
	} else {
		fmt.Fprintf(e.out, "%s is running (no port)\n", v.Name)
	}
	return nil
}

func cmdLogs(e *env, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	n := fs.Int("n", 200, "")
	follow := fs.Bool("f", false, "")
	raw := fs.Bool("raw", false, "")
	match := fs.String("grep", "", "")
	around := fs.Int("C", 0, "")
	stamped := fs.Bool("t", false, "")
	since := fs.String("since", "", "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return usageError{"logs takes exactly one server or container name"}
	}
	if (*match != "" || *stamped || *since != "") && (*follow || *raw) {
		return usageError{"--grep, -t and --since cannot be combined with -f or --raw"}
	}
	q := url.Values{"lines": {strconv.Itoa(*n)}}
	if *match != "" {
		q.Set("match", *match)
		q.Set("context", strconv.Itoa(*around))
	}
	if *stamped {
		q.Set("timestamps", "1")
	}
	if *since != "" {
		q.Set("since", *since)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var st server.State
	if err := c.Do(e.ctx, http.MethodGet, "/api/state", nil, &st); err != nil {
		return err
	}
	name := names[0]
	base := "/api/servers/" + url.PathEscape(name)
	if !slices.ContainsFunc(st.Servers, func(v supervisor.View) bool { return v.Name == name }) &&
		slices.ContainsFunc(st.Docker.Containers, func(d docker.Container) bool { return d.Name == name }) {
		return containerLogs(e, c, name, q, *follow)
	}
	if !*follow {
		if *raw {
			q.Set("raw", "1")
		}
		return c.Do(e.ctx, http.MethodGet, base+"/logs?"+q.Encode(), nil, e.out)
	}
	// Follow: scrollback and live output come over one subscription, so
	// nothing printed in between is lost. The first message is the scrollback.
	ws, err := c.DialWebSocket(e.ctx, "/api/servers/"+url.PathEscape(name)+"/terminal?always=1&readonly=1")
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(-1)
	if _, backlog, err := ws.Read(e.ctx); err == nil {
		for _, l := range supervisor.PlainLines(backlog, *n) {
			fmt.Fprintln(e.out, l)
		}
	}
	isTTY := isTerminal(os.Stdout)
	for {
		_, data, err := ws.Read(e.ctx)
		if err != nil {
			return nil
		}
		if !*raw && (!isTTY || !e.color) {
			data = []byte(supervisor.StripANSI(strings.ReplaceAll(string(data), "\r\n", "\n")))
		}
		_, _ = e.out.Write(data)
	}
}

func cmdOpen(e *env, args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	alias := fs.Bool("alias", false, "")
	printOnly := fs.Bool("print", false, "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	next := "/"
	if len(names) > 0 {
		st, err := e.state()
		if err != nil {
			return err
		}
		for _, s := range st.Servers {
			if s.Name != names[0] {
				continue
			}
			// A server's own address needs no sign-in.
			target := s.URL
			if *alias {
				target = s.Alias
			}
			if target != "" {
				fmt.Fprintln(e.out, target)
				if *printOnly {
					return nil
				}
				if argv := browserCommand(runtime.GOOS, os.Getenv, target); argv != nil {
					return exec.Command(argv[0], argv[1:]...).Start()
				}
				return nil
			}
			next = "/#/" + url.PathEscape(s.Name)
		}
		if next == "/" {
			return fmt.Errorf("unknown server %q", names[0])
		}
	}
	// The dashboard needs a session: exchange the token for a single-use
	// link. The link shows in the opener's arguments, so it is on
	// 127.0.0.1 and does not name the dashboard host; the daemon forwards
	// and redeems it only on a connection from this account, and only once.
	var link struct {
		URL       string `json:"url"`
		Dashboard string `json:"dashboard"`
	}
	if err := c.Do(e.ctx, http.MethodPost, "/api/auth/code", map[string]any{"next": next}, &link); err != nil {
		return err
	}
	u, err := url.Parse(link.URL)
	d, derr := url.Parse(link.Dashboard)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || derr != nil || !strings.HasSuffix(d.Hostname(), ".localhost") {
		return fmt.Errorf("the daemon gave no usable sign-in link; restart it with `%s daemon restart`", app.ID)
	}
	// Print where the dashboard opens; the link only where no browser
	// here can take it.
	fmt.Fprintln(e.out, d.Scheme+"://"+d.Host+next)
	var argv []string
	if !*printOnly {
		argv = browserCommand(runtime.GOOS, os.Getenv, link.URL)
	}
	if argv != nil {
		return exec.Command(argv[0], argv[1:]...).Start()
	}
	fmt.Fprintf(e.out, "\nOpen this link to sign in; it works once, within two minutes, from your account:\n%s\n", link.URL)
	if os.Getenv("SSH_CONNECTION") != "" {
		fmt.Fprintf(e.out, "\nOver SSH, forward the port first and open the link on your own computer, where *.localhost resolves to it:\n  ssh -L %[1]s:127.0.0.1:%[1]s <this host>\n", u.Port())
	}
	return nil
}

func cmdPorts(e *env, args []string) error {
	fs := flag.NewFlagSet("ports", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var res struct {
		Listeners []server.ListenerView `json:"listeners"`
	}
	if err := c.Do(e.ctx, http.MethodGet, "/api/listeners", nil, &res); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(e, res.Listeners)
	}
	for _, l := range res.Listeners {
		owner := e.paint(dim, "unmanaged")
		if l.Managed {
			owner = e.paint(green, l.Server)
			if l.Via != "" {
				owner += e.paint(dim, " (via "+l.Via+")")
			}
		} else if l.Server != "" {
			owner = e.paint(yellow, "blocks "+l.Server)
		}
		fmt.Fprintf(e.out, "  :%-6d %-7d %-18s %-28s %s\n", l.Port, l.PID, truncate(l.Command, 18), truncate(l.CwdDisplay, 28), owner)
	}
	return nil
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
