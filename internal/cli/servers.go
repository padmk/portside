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
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

func (e *env) state() (*server.State, error) {
	c, err := e.client()
	if err != nil {
		return nil, err
	}
	var st server.State
	if err := c.Do(e.ctx, http.MethodGet, "/api/state", nil, &st); err != nil {
		return nil, err
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
	if st.Config.Error != "" {
		fmt.Fprintf(e.errOut, "%s %s\n\n", e.paint(red, "config error:"), st.Config.Error)
	}
	if len(st.Servers) == 0 {
		fmt.Fprintf(e.out, "No servers yet. Add one with `portside add`, find some with `portside scan`,\nor open the dashboard: http://127.0.0.1:%d\n", st.Daemon.Port)
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
	group := "\x00"
	for _, s := range ordered {
		if s.Group != group && len(st.Groups) > 0 {
			group = s.Group
			label := s.Group
			if label == "" {
				label = "ungrouped"
			}
			fmt.Fprintf(e.out, "%s\n", e.paint(dim, strings.ToUpper(label)))
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
		}
		if s.Stale {
			note = strings.TrimSpace(note + " config changed, restart to apply")
		}
		fmt.Fprintf(e.out, "  %s %-*s  %-9s %s  %-16s %s\n",
			e.statusDot(s), nameW, s.Name, e.statusWord(s), port, usage, e.paint(dim, note))
	}
	fmt.Fprintf(e.out, "\n%s http://127.0.0.1:%d\n", e.paint(dim, "dashboard"), st.Daemon.Port)
	return nil
}

func (e *env) statusDot(s supervisor.View) string {
	switch s.Status {
	case supervisor.Running:
		return e.paint(green, "●")
	case supervisor.Starting, supervisor.Stopping:
		return e.paint(yellow, "◐")
	case supervisor.Crashed, supervisor.Blocked:
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
	timeout := fs.String("timeout", "90s", "")
	asJSON := fs.Bool("json", false, "")
	takeover := fs.Bool("takeover", false, "")
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
	var v supervisor.View
	err = c.Do(e.ctx, http.MethodPost, "/api/servers/"+url.PathEscape(names[0])+"/ensure",
		map[string]any{"timeout": *timeout, "takeover": *takeover}, &v)
	if err != nil {
		var apiErr *client.APIError
		if *asJSON && errors.As(err, &apiErr) && len(apiErr.Body) > 0 {
			_, _ = e.out.Write(apiErr.Body)
			return silentError{1}
		}
		return err
	}
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
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return usageError{"logs takes exactly one server name"}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	name := names[0]
	if !*follow {
		q := fmt.Sprintf("/api/servers/%s/logs?lines=%d", url.PathEscape(name), *n)
		if *raw {
			q += "&raw=1"
		}
		return c.Do(e.ctx, http.MethodGet, q, nil, e.out)
	}
	// Follow: scrollback and live output come over one subscription, so
	// nothing printed in between is lost. The first message is the scrollback.
	ws, _, err := websocket.Dial(e.ctx, "ws://127.0.0.1:"+fmt.Sprint(c.Port)+"/api/servers/"+url.PathEscape(name)+"/terminal?always=1&readonly=1",
		&websocket.DialOptions{HTTPHeader: c.Header()})
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
				return openURL(target)
			}
			next = "/#/" + url.PathEscape(s.Name)
		}
		if next == "/" {
			return fmt.Errorf("unknown server %q", names[0])
		}
	}
	// The dashboard needs a session: exchange the token for a one-time link.
	var link struct {
		URL string `json:"url"`
	}
	if err := c.Do(e.ctx, http.MethodPost, "/api/auth/code", map[string]string{"next": next}, &link); err != nil {
		return err
	}
	fmt.Fprintln(e.out, c.Base+next)
	return openURL(link.URL)
}

func openURL(u string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", u).Start()
	}
	return exec.Command("xdg-open", u).Start()
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

// waitGone polls until the daemon stops answering.
func waitGone(c *client.Client, e *env, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := c.Ping(e.ctx); err != nil {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
