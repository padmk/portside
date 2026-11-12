// Package act runs terminal UI actions without blocking the UI. Each
// function captures what its request needs (the client, the target) when it
// is called, so the command it returns never reads the shared environment
// from its goroutine.
package act

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/browser"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
)

func request(c *client.Client, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.Do(ctx, method, path, body, out)
}

func msg(m tea.Msg) tea.Cmd { return func() tea.Msg { return m } }

func notice(level ui.Level, text string) tea.Cmd {
	return msg(ui.NoticeMsg{Level: level, Text: text})
}

// Progressive is an operation's verb while its request runs.
func Progressive(verb string) string {
	switch verb {
	case "start":
		return "starting"
	case "stop":
		return "stopping"
	case "restart":
		return "restarting"
	}
	return verb
}

// Server runs start, stop or restart on one server as operation id; the
// result comes when the request returns.
func Server(c *client.Client, id int, verb, name string) tea.Cmd {
	return func() tea.Msg {
		body := struct {
			Takeover bool `json:"takeover"`
		}{}
		err := request(c, http.MethodPost, "/api/servers/"+url.PathEscape(name)+"/"+verb, body, nil)
		return ui.OpDoneMsg{ID: id, Label: verb + " " + name, Items: []ui.Selection{ui.ServerItem(name)}, Err: err}
	}
}

// Group runs start, stop or restart on the named members of a group in one
// bulk request as operation id; the daemon orders them by dependencies.
func Group(c *client.Client, id int, verb, group string, names []string) tea.Cmd {
	items := make([]ui.Selection, len(names))
	for i, n := range names {
		items[i] = ui.ServerItem(n)
	}
	label := verb + " " + ui.GroupLabel(group)
	return func() tea.Msg {
		var result struct {
			Results []struct {
				Name  string `json:"name"`
				Error string `json:"error"`
			} `json:"results"`
		}
		body := struct {
			Action string   `json:"action"`
			Names  []string `json:"names"`
		}{verb, names}
		err := request(c, http.MethodPost, "/api/bulk", body, &result)
		var errs []string
		for _, r := range result.Results {
			if r.Error != "" {
				errs = append(errs, r.Name+": "+r.Error)
			}
		}
		return ui.OpDoneMsg{ID: id, Label: label, Items: items, Err: err, Errs: errs}
	}
}

func noticeRequest(c *client.Client, method, path string, body any, verb, name, success string) tea.Cmd {
	return func() tea.Msg {
		if err := request(c, method, path, body, nil); err != nil {
			return ui.NoticeMsg{Level: ui.LevelError, Text: fmt.Sprintf("%s %s: %v", verb, name, err)}
		}
		return ui.NoticeMsg{Level: ui.LevelSuccess, Text: success}
	}
}

// SetEnabled turns a server on or off for automatic starts.
func SetEnabled(env *ui.Env, name string, on bool) tea.Cmd {
	state := "disabled"
	verb := "disable"
	if on {
		state = "enabled"
		verb = "enable"
	}
	return noticeRequest(env.Client, http.MethodPost, "/api/servers/"+url.PathEscape(name)+"/enabled", struct {
		Enabled bool `json:"enabled"`
	}{on}, verb, name, name+" "+state)
}

// Delete removes a server from the config; the caller confirms first.
func Delete(env *ui.Env, name string) tea.Cmd {
	return noticeRequest(env.Client, http.MethodDelete, "/api/servers/"+url.PathEscape(name), nil, "delete", name, "deleted "+name)
}

// remoteOnly is the notice for an action that would open something on the
// wrong computer.
func remoteOnly(env *ui.Env, what string) tea.Cmd {
	return notice(ui.LevelWarn, what+" works only on this computer; this session is on "+env.Session.Computer)
}

// OpenOnDesktop asks the daemon to open the server's folder in the editor
// ("editor") or a terminal there ("terminal") on its desktop; a session on
// another computer cannot.
func OpenOnDesktop(env *ui.Env, name, kind string) tea.Cmd {
	if env.Session.Remote() {
		return remoteOnly(env, "opening apps")
	}
	return noticeRequest(env.Client, http.MethodPost, "/api/open", struct {
		Target string `json:"target"`
		Name   string `json:"name"`
	}{kind, name}, "open", name, "opened "+name+" in "+kind)
}

// ShellDoneMsg reports that the shell of Shell ended.
type ShellDoneMsg struct {
	Server string
	Err    error
}

// Shell suspends the terminal UI and runs the user's shell ($SHELL, else
// /bin/sh) in the server's directory, in this terminal, until it exits
// or the program ends; it works over SSH.
// A session on another computer has no such directory here.
func Shell(env *ui.Env, v supervisor.View) tea.Cmd {
	if env.Session.Remote() {
		return remoteOnly(env, "the shell")
	}
	if v.Dir == "" {
		return notice(ui.LevelError, v.Name+" has no directory")
	}
	if st, err := os.Stat(v.Dir); err != nil || !st.IsDir() {
		return notice(ui.LevelError, "shell: "+v.DirDisplay+" is not a directory here")
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	c := exec.Command(sh)
	c.Dir = v.Dir
	name := v.Name
	// The shell ends with the program (ui.Process): the UI then quits.
	return ui.Exec(&ui.Process{Ctx: env.ProgramContext(), Cmd: c}, func(err error) tea.Msg { return ShellDoneMsg{Server: name, Err: err} })
}

// ShellResult is the notice for a shell that ended; a shell's own exit
// status is not an error of the terminal UI.
func ShellResult(m ShellDoneMsg) ui.NoticeMsg {
	var exit *exec.ExitError
	if m.Err != nil && !errors.As(m.Err, &exit) {
		return ui.NoticeMsg{Level: ui.LevelError, Text: "shell in " + m.Server + ": " + m.Err.Error()}
	}
	return ui.NoticeMsg{Level: ui.LevelInfo, Text: "back from the shell in " + m.Server}
}

// CopyURL copies the server's URL, as it opens from here, to the
// clipboard (OSC 52) with a notice.
func CopyURL(env *ui.Env, v supervisor.View) tea.Cmd {
	return copyURL(env.Session.URL(v.Name, v.URL))
}

func copyURL(u string) tea.Cmd {
	if u == "" {
		return notice(ui.LevelError, "no URL available")
	}
	return tea.Batch(tea.SetClipboard(u), notice(ui.LevelSuccess, "copied "+u))
}

// OpenURL opens the server's URL, or its alias, as it opens from here in a
// browser of this session; without one it copies the address instead.
func OpenURL(env *ui.Env, v supervisor.View, alias bool) tea.Cmd {
	u := v.URL
	if alias {
		u = v.Alias
	}
	u = env.Session.URL(v.Name, u)
	if u == "" {
		return notice(ui.LevelError, "open "+v.Name+": no URL available")
	}
	args := browser.Command(runtime.GOOS, os.Getenv, u)
	if len(args) == 0 {
		return copyURL(u)
	}
	name := v.Name
	return func() tea.Msg {
		if err := exec.Command(args[0], args[1:]...).Start(); err != nil {
			return ui.NoticeMsg{Level: ui.LevelError, Text: fmt.Sprintf("open %s: %v", name, err)}
		}
		return ui.NoticeMsg{Level: ui.LevelSuccess, Text: "opened " + name + " in the browser"}
	}
}

// ConfirmDelete asks before removing a server from the config, saying
// that a running one is stopped first; a temporary copy, which is not in
// the config, gets a hint instead.
func ConfirmDelete(env *ui.Env, v supervisor.View) tea.Cmd {
	if v.Temporary {
		return notice(ui.LevelWarn, v.Name+" is a temporary copy, not in the config; stop it to remove it.")
	}
	body := "Remove " + v.Name + " from the config?"
	if v.HasProcess() {
		body += " It is stopped first."
	}
	return msg(ui.ConfirmMsg{
		Title:   "Delete server",
		Body:    body,
		Confirm: "Delete",
		Yes:     Delete(env, v.Name),
	})
}

// ConfirmClearLogs asks before clearing a server's scrollback, which the
// daemon keeps in memory; its log file stays.
func ConfirmClearLogs(env *ui.Env, name string) tea.Cmd {
	c := env.Client
	yes := func() tea.Msg {
		return ui.LogsClearedMsg{Server: name, Err: request(c, http.MethodDelete, "/api/servers/"+url.PathEscape(name)+"/logs", nil, nil)}
	}
	return msg(ui.ConfirmMsg{
		Title:   "Clear logs",
		Body:    "Clear the daemon's scrollback of " + name + "? Its log file is kept.",
		Confirm: "Clear",
		Yes:     yes,
	})
}
