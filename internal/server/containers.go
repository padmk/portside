package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/supervisor"
)

func (s *Server) containerRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/docker/start", s.startEngine)
	m.HandleFunc("GET /api/containers/{ref}", s.containerDetails)
	m.HandleFunc("POST /api/containers/{ref}/start", s.containerAction("start"))
	m.HandleFunc("POST /api/containers/{ref}/stop", s.containerAction("stop"))
	m.HandleFunc("POST /api/containers/{ref}/restart", s.containerAction("restart"))
	m.HandleFunc("POST /api/containers/{ref}/recreate", s.recreate)
	m.HandleFunc("DELETE /api/containers/{ref}/job", s.dismissJob)
	m.HandleFunc("GET /api/containers/{ref}/job/terminal", s.jobTerminal)
	m.HandleFunc("GET /api/containers/{ref}/logs", s.containerLogs)
	m.HandleFunc("GET /api/containers/{ref}/shell", s.containerShell)
}

// dockerState is the engine state with every container attributed to the
// server whose directory is its compose project directory.
func (s *Server) dockerState() docker.State {
	st := s.opt.Docker.Snapshot()
	cfg := s.sup.Config()
	// Servers sharing a directory: the one running compose, else the first.
	byDir := map[string]string{}
	compose := map[string]bool{}
	for _, sv := range cfg.Servers {
		d := canonicalDir(sv.Dir)
		isCompose := supervisor.IsComposeCommand(sv.Cmd)
		if _, ok := byDir[d]; !ok || (isCompose && !compose[d]) {
			byDir[d] = sv.Name
			compose[d] = isCompose
		}
	}
	for i := range st.Containers {
		c := &st.Containers[i]
		if c.WorkingDir != "" {
			c.Server = byDir[canonicalDir(c.WorkingDir)]
			c.WorkingDir = paths.ShortenHome(c.WorkingDir)
		}
	}
	return st
}

var canonicalDirs sync.Map // path -> path with symlinks resolved

// canonicalDir resolves symlinks (such as /tmp on macOS) so a compose
// project directory matches the server directory however either was written.
func canonicalDir(p string) string {
	p = filepath.Clean(p)
	if v, ok := canonicalDirs.Load(p); ok {
		return v.(string)
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p // not cached: the directory may appear later
	}
	canonicalDirs.Store(p, r)
	return r
}

// containerRef resolves {ref} (a container name or ID) or answers 404.
func (s *Server) containerRef(w http.ResponseWriter, r *http.Request) (docker.Container, bool) {
	c, ok := s.opt.Docker.Find(r.PathValue("ref"))
	if !ok {
		fail(w, http.StatusNotFound, fmt.Errorf("no container %q", r.PathValue("ref")))
	}
	return c, ok
}

func (s *Server) startEngine(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.Docker.StartEngine(); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) containerAction(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.containerRef(w, r)
		if !ok {
			return
		}
		cl, err := s.opt.Docker.Client()
		if err != nil {
			fail(w, http.StatusServiceUnavailable, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		switch kind {
		case "start":
			err = cl.Start(ctx, c.ID)
		case "stop":
			err = cl.Stop(ctx, c.ID)
		case "restart":
			err = cl.Restart(ctx, c.ID)
		}
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.opt.Docker.Kick()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) recreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Build bool `json:"build"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	c, ok := s.containerRef(w, r)
	if !ok {
		return
	}
	if err := s.opt.Docker.Recreate(c.Name, body.Build); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) dismissJob(w http.ResponseWriter, r *http.Request) {
	s.opt.Docker.DismissJob(r.PathValue("ref"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) jobTerminal(w http.ResponseWriter, r *http.Request) {
	lb, ok := s.opt.Docker.JobLog(r.PathValue("ref"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("no job for this container"))
		return
	}
	streamBuffer(w, r, lb, nil)
}

// ContainerDetails is what the dashboard shows about one container.
type ContainerDetails struct {
	Name          string            `json:"name"`
	ID            string            `json:"id"`
	Image         string            `json:"image"`
	Command       string            `json:"command"`
	Created       time.Time         `json:"created"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	Health        *HealthDetails    `json:"health,omitempty"`
	Error         string            `json:"error,omitempty"`
	Env           []EnvVar          `json:"env"`
	Mounts        []MountView       `json:"mounts"`
	Networks      []NetworkView     `json:"networks"`
	ConfigFiles   []string          `json:"configFiles,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// HealthDetails is the latest health check result.
type HealthDetails struct {
	Status        string `json:"status"`
	FailingStreak int    `json:"failingStreak"`
	Output        string `json:"output,omitempty"`
	ExitCode      int    `json:"exitCode"`
}

// EnvVar is one environment variable; the dashboard masks secret-looking ones.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MountView is a volume or bind mount.
type MountView struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
}

// NetworkView is one network the container is attached to.
type NetworkView struct {
	Name    string   `json:"name"`
	IP      string   `json:"ip,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

func (s *Server) containerDetails(w http.ResponseWriter, r *http.Request) {
	c, ok := s.containerRef(w, r)
	if !ok {
		return
	}
	cl, err := s.opt.Docker.Client()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, err)
		return
	}
	in, err := cl.Inspect(r.Context(), c.ID)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	d := ContainerDetails{
		Name:          strings.TrimPrefix(in.Name, "/"),
		ID:            in.ID,
		Image:         in.Config.Image,
		Command:       shellJoin(append(append([]string{}, in.Config.Entrypoint...), in.Config.Cmd...)),
		Created:       in.Created,
		RestartPolicy: in.HostConfig.RestartPolicy.Name,
		Error:         in.State.Error,
		Env:           []EnvVar{},
		Mounts:        []MountView{},
		Networks:      []NetworkView{},
	}
	if h := in.State.Health; h != nil {
		hd := &HealthDetails{Status: h.Status, FailingStreak: h.FailingStreak}
		if n := len(h.Log); n > 0 {
			hd.Output = strings.TrimSpace(h.Log[n-1].Output)
			hd.ExitCode = h.Log[n-1].ExitCode
		}
		d.Health = hd
	}
	for _, kv := range in.Config.Env {
		k, v, _ := strings.Cut(kv, "=")
		d.Env = append(d.Env, EnvVar{Key: k, Value: v})
	}
	sort.Slice(d.Env, func(i, j int) bool { return d.Env[i].Key < d.Env[j].Key })
	for _, mt := range in.Mounts {
		src := mt.Source
		if mt.Type == "bind" {
			src = paths.ShortenHome(src)
		}
		d.Mounts = append(d.Mounts, MountView{Type: mt.Type, Name: mt.Name, Source: src, Destination: mt.Destination, ReadOnly: !mt.RW})
	}
	for name, n := range in.NetworkSettings.Networks {
		var aliases []string
		for _, a := range n.Aliases {
			// Engines add the short container ID as an alias; it says nothing.
			if !strings.HasPrefix(in.ID, a) {
				aliases = append(aliases, a)
			}
		}
		d.Networks = append(d.Networks, NetworkView{Name: name, IP: n.IPAddress, Aliases: aliases})
	}
	sort.Slice(d.Networks, func(i, j int) bool { return d.Networks[i].Name < d.Networks[j].Name })
	for _, f := range strings.Split(in.Config.Labels["com.docker.compose.project.config_files"], ",") {
		if f = strings.TrimSpace(f); f != "" {
			d.ConfigFiles = append(d.ConfigFiles, paths.ShortenHome(f))
		}
	}
	writeJSON(w, http.StatusOK, d)
}

// shellJoin quotes arguments that need it, for display.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'$`\\|&;<>()*?[]#~") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// containerLogs streams a container's output to a terminal. It follows the
// container by name across stops, starts and recreation, marking each with
// a dim separator like the server terminal does.
func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request) {
	first, ok := s.containerRef(w, r)
	if !ok {
		return
	}
	name := first.Name
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go readTermMsgs(ctx, cancel, ws, nil)

	send := func(b []byte) error {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageBinary, b)
	}
	marker := func(at time.Time, what string) error {
		return send([]byte(fmt.Sprintf("\r\n\x1b[2m── %s  %s ──\x1b[0m\r\n", at.Local().Format("15:04:05"), what)))
	}
	changes := s.opt.Docker.Subscribe()
	defer s.opt.Docker.Unsubscribe(changes)

	var since time.Time
	// shownID and shownState are what the terminal last reflected; a change
	// opens a new stream.
	shownID, shownState := "", ""
	for ctx.Err() == nil {
		c, found := s.opt.Docker.Find(name)
		cl, cerr := s.opt.Docker.Client()
		if found && cerr == nil && (c.ID != shownID || c.State != shownState) {
			switch {
			case shownID != "" && c.ID != shownID:
				since = time.Time{}
				if marker(time.Now(), "recreated") != nil {
					return
				}
			case shownState != "" && c.State == "running":
				if marker(time.Now(), "start") != nil {
					return
				}
			}
			shownID, shownState = c.ID, c.State
			tail := "all"
			if since.IsZero() {
				tail = "2000"
			}
			opened := time.Now()
			stream, err := cl.Logs(ctx, c.ID, c.TTY, tail, since, true)
			if err != nil {
				// Try again on the next change or tick.
				shownState = ""
			} else {
				// Resume from the last output that arrived, so nothing written
				// while a dropped stream went unnoticed is skipped.
				last := opened
				_ = pump(stream, !c.TTY, func(b []byte) error {
					last = time.Now()
					return send(b)
				})
				stream.Close()
				if last.After(since) {
					since = last
				}
				if ctx.Err() != nil {
					return
				}
				// The stream ends when the container stops. Give the monitor a
				// moment to see that before deciding what happened.
				time.Sleep(300 * time.Millisecond)
				cur, ok := s.opt.Docker.Find(name)
				switch {
				case ok && cur.ID == c.ID && (cur.State == "exited" || cur.State == "dead"):
					shownState = cur.State
					what, at := "stopped", time.Now()
					if cur.ExitCode != nil && *cur.ExitCode != 0 {
						what = fmt.Sprintf("exited (code %d)", *cur.ExitCode)
					}
					if cur.FinishedAt != nil {
						at = *cur.FinishedAt
					}
					if marker(at, what) != nil {
						return
					}
				case ok && cur.ID == c.ID && cur.State != "running":
					shownState = cur.State
				default:
					// Dropped while running, or the engine went away: resume
					// quietly once it is back.
					shownState = ""
					if ok && cur.ID == c.ID {
						shownState = "resume"
					}
				}
				if shownState == "resume" {
					shownState = ""
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
				}
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-changes:
		case <-time.After(5 * time.Second):
		}
	}
}

// pump copies a log stream to send in chunks, turning bare line feeds into
// CRLF for output that did not come from a TTY.
func pump(rd io.Reader, crlf bool, send func([]byte) error) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if crlf {
				chunk = bytes.ReplaceAll(chunk, []byte("\n"), []byte("\r\n"))
			} else {
				chunk = append([]byte(nil), chunk...)
			}
			if werr := send(chunk); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}

// shellCommand starts the best shell an image has.
var shellCommand = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}

// shellSessionVar tags every process of a shell session, so the session can
// be ended when its terminal goes away: the engine keeps an exec running
// after its client disconnects.
const shellSessionVar = "EXEC_SESSION"

// endShell hangs up the processes of a shell session that is still running,
// then kills what is left.
func endShell(cl *docker.Client, containerID, execID, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, sig := range []string{"HUP", "KILL"} {
		if running, err := cl.ExecRunning(ctx, execID); err != nil || !running {
			return
		}
		script := `for p in /proc/[0-9]*; do if tr '\0' '\n' < "$p/environ" 2>/dev/null | grep -qx "$1"; then kill -` + sig + ` "${p#/proc/}" 2>/dev/null; fi; done`
		if cl.ExecDetached(ctx, containerID, []string{"/bin/sh", "-c", script, "sh", shellSessionVar + "=" + token}) != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// containerShell runs an interactive shell in a running container and
// bridges its TTY to a terminal over the WebSocket.
func (s *Server) containerShell(w http.ResponseWriter, r *http.Request) {
	c, ok := s.containerRef(w, r)
	if !ok {
		return
	}
	cl, err := s.opt.Docker.Client()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, err)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 16)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	send := func(b []byte) error {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageBinary, b)
	}
	failed := func(msg string) {
		_ = send([]byte("\x1b[31m" + msg + "\x1b[0m\r\n"))
		ws.Close(websocket.StatusNormalClosure, "")
	}
	if c.State != "running" {
		failed("The container is not running.")
		return
	}
	token := rand.Text()
	execID, err := cl.ExecCreate(ctx, c.ID, shellCommand, []string{"TERM=xterm-256color", "COLORTERM=truecolor", shellSessionVar + "=" + token})
	if err != nil {
		failed(err.Error())
		return
	}
	conn, out, err := cl.ExecAttach(ctx, execID)
	if err != nil {
		failed(err.Error())
		return
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go readTermMsgs(ctx, cancel, ws, func(m termMsg) {
		switch m.T {
		case "i":
			_, _ = conn.Write([]byte(m.D))
		case "r":
			if m.Cols >= 20 && m.Rows >= 5 && m.Cols <= 500 && m.Rows <= 300 {
				rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
				_ = cl.ExecResize(rctx, execID, m.Cols, m.Rows)
				rcancel()
			}
		}
	})
	_ = pump(out, false, send)
	if ctx.Err() == nil {
		_ = send([]byte("\r\n\x1b[2m── session ended ──\x1b[0m\r\n"))
		ws.Close(websocket.StatusNormalClosure, "")
		return
	}
	go endShell(cl, c.ID, execID, token)
}
