package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/config"
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
	m.HandleFunc("POST /api/containers/{ref}/project/{action}", s.containerProjectAction)
	m.HandleFunc("DELETE /api/containers/{ref}/job", s.dismissJob)
	m.HandleFunc("GET /api/containers/{ref}/job/terminal", s.jobTerminal)
	m.HandleFunc("GET /api/containers/{ref}/logs", s.containerLogs)
	m.HandleFunc("GET /api/containers/{ref}/shell", s.containerShell)
}

// dockerState attaches containers to their configured servers and marks issues.
func (s *Server) dockerState(views []supervisor.View) docker.State {
	st := s.opt.Docker.Snapshot()
	defs := s.sup.Definitions()
	attachContainers(st.Containers, defs)
	markNetworkPorts(st.Containers, defs)
	for i := range st.Containers {
		c := &st.Containers[i]
		c.IssueFor = issueFor(c, views)
		if c.WorkingDir != "" {
			c.WorkingDir = paths.ShortenHome(c.WorkingDir)
		}
	}
	return st
}

// markNetworkPorts flags published ports reachable from the network unless
// the container's owning server sets `lan = true`.
func markNetworkPorts(containers []docker.Container, servers []config.Server) {
	for i := range containers {
		c := &containers[i]
		lan := slices.ContainsFunc(servers, func(d config.Server) bool { return d.Name == c.Server && d.LAN })
		for j := range c.Ports {
			c.Ports[j].NetworkWarning = supervisor.NetworkWarning(c.Ports[j].Exposure, lan)
		}
	}
}

// issueFor lists the servers a container needs attention for: it is
// unhealthy or restarting, or it failed while the server ran.
func issueFor(c *docker.Container, views []supervisor.View) []string {
	var out []string
	for _, name := range c.Servers() {
		i := slices.IndexFunc(views, func(v supervisor.View) bool { return v.Name == name })
		if i < 0 {
			continue
		}
		v := views[i]
		alive := v.Status == supervisor.Running || v.Status == supervisor.Starting
		switch {
		case c.State == "running" && c.Health == "unhealthy", c.State == "restarting":
			out = append(out, name)
		case alive && c.Failed && (c.FinishedAt == nil || v.StartedAt == nil || !c.FinishedAt.Before(*v.StartedAt)):
			out = append(out, name)
		}
	}
	return out
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
			if c.State == "running" || c.State == "paused" || c.State == "restarting" {
				s.opt.Docker.RecordStop(c.ID)
			}
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
		Pull  bool `json:"pull"`
		Wait  bool `json:"wait"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	c, ok := s.containerRef(w, r)
	if !ok {
		return
	}
	job, err := s.opt.Docker.Recreate(c.Name, body.Build, body.Pull)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if body.Wait {
		s.writeContainerJob(w, r, job)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) containerProjectAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	action := r.PathValue("action")
	if action == "down" && !body.Confirm {
		fail(w, http.StatusBadRequest, errors.New("confirm removal of project containers; volumes are kept"))
		return
	}
	c, ok := s.containerRef(w, r)
	if !ok {
		return
	}
	job, err := s.opt.Docker.ProjectAction(c.Name, action)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.writeContainerJob(w, r, job)
}

func (s *Server) writeContainerJob(w http.ResponseWriter, r *http.Request, job *docker.Job) {
	result, err := job.Wait(r.Context())
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.containerLogText(w, r, first)
		return
	}
	q, err := parseLogQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	initialTail := 2000
	if r.URL.Query().Has("lines") {
		initialTail = q.lines
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
	sendStatus := func(message any) error {
		data, _ := json.Marshal(message)
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageText, data)
	}
	status := func(kind, message string) error { return sendStatus(map[string]string{"t": kind, "d": message}) }
	marker := func(at time.Time, what string) error {
		return send([]byte(fmt.Sprintf("\r\n\x1b[2m── %s  %s ──\x1b[0m\r\n", at.Local().Format("15:04:05"), what)))
	}
	changes := s.opt.Docker.Subscribe()
	defer s.opt.Docker.Unsubscribe(changes)

	since := q.since
	if id := r.URL.Query().Get("id"); id != "" && id != first.ID {
		since = time.Time{}
	}
	firstStream := true
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
			if firstStream || since.IsZero() {
				tail = strconv.Itoa(initialTail)
			}
			opened := time.Now()
			stream, err := cl.Logs(ctx, c.ID, c.TTY, tail, since, true, false)
			if err != nil {
				if status("error", err.Error()) != nil {
					return
				}
				// Try again on the next change or tick.
				shownState = ""
			} else {
				// Resume from the last output that arrived, so nothing written
				// while a dropped stream went unnoticed is skipped.
				if status("status", "") != nil {
					stream.Close()
					return
				}
				firstStream = false
				last := opened
				streamErr := pump(stream, !c.TTY, func(b []byte) error {
					last = time.Now()
					if err := send(b); err != nil {
						return err
					}
					return sendStatus(map[string]string{"t": "cursor", "d": last.Format(time.RFC3339Nano), "id": c.ID})
				})
				stream.Close()
				if streamErr != nil && !errors.Is(streamErr, io.EOF) && ctx.Err() == nil {
					if status("error", streamErr.Error()) != nil {
						return
					}
				}
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
		if cerr != nil {
			if status("error", cerr.Error()) != nil {
				return
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

// containerLogText answers a plain request for a container's logs with
// text, like a server's logs. A search covers the last searchTail lines,
// and at most maxLogRead bytes of the newest output are read.
func (s *Server) containerLogText(w http.ResponseWriter, r *http.Request, c docker.Container) {
	q, err := parseLogQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	cl, err := s.opt.Docker.Client()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, err)
		return
	}
	const searchTail = 20000
	tail := q.lines
	if q.match != nil {
		tail = searchTail
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	stream, err := cl.Logs(ctx, c.ID, c.TTY, strconv.Itoa(tail+q.context), q.since, false, true)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	defer stream.Close()
	data, cut, err := readTail(stream, maxLogRead)
	var notes []string
	if q.match != nil {
		notes = append(notes, fmt.Sprintf("searched the last %d lines", searchTail))
	}
	if cut {
		notes = append(notes, fmt.Sprintf("only the newest %d MiB of output were read", maxLogRead>>20))
	}
	if err != nil {
		if len(data) == 0 {
			fail(w, http.StatusBadGateway, err)
			return
		}
		notes = append(notes, "the log stream broke off: "+err.Error())
	}
	writeLines(w, q, stampedLines(data), notes...)
}

// stampedLines splits engine output requested with timestamps into plain
// lines and their times.
func stampedLines(data []byte) []supervisor.Line {
	raw := strings.Split(string(data), "\n")
	out := make([]supervisor.Line, 0, len(raw))
	for _, l := range raw {
		var line supervisor.Line
		if stamp, rest, ok := strings.Cut(l, " "); ok {
			if at, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				line.At, l = at.Local(), rest
			}
		}
		line.Text = supervisor.PlainLine(l)
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1].Text) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// maxLogRead bounds how much container output one plain request reads.
const maxLogRead = 16 << 20

// readTail reads rd to the end and keeps its last limit bytes, reporting
// whether anything earlier was dropped.
func readTail(rd io.Reader, limit int) ([]byte, bool, error) {
	var buf []byte
	cut := false
	chunk := make([]byte, 64<<10)
	for {
		n, err := rd.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if len(buf) > 2*limit {
			buf = append(buf[:0], buf[len(buf)-limit:]...)
			cut = true
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return trim(buf, limit, &cut), cut, err
		}
	}
	return trim(buf, limit, &cut), cut, nil
}

func trim(buf []byte, limit int, cut *bool) []byte {
	if len(buf) > limit {
		*cut = true
		buf = buf[len(buf)-limit:]
	}
	return buf
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
