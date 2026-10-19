package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/discover"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/supervisor"
)

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("GET /api/session", s.session)
	m.HandleFunc("POST /api/auth/code", s.authCode)
	m.HandleFunc("GET /auth", s.signIn)
	m.HandleFunc("GET /api/state", s.state)
	m.HandleFunc("GET /api/events", s.events)

	m.HandleFunc("POST /api/servers/{name}/start", s.action("start"))
	m.HandleFunc("POST /api/servers/{name}/stop", s.action("stop"))
	m.HandleFunc("POST /api/servers/{name}/restart", s.action("restart"))
	m.HandleFunc("POST /api/servers/{name}/ensure", s.ensure)
	m.HandleFunc("POST /api/bulk", s.bulk)

	m.HandleFunc("GET /api/servers/{name}/logs", s.logs)
	m.HandleFunc("DELETE /api/servers/{name}/logs", s.clearLogs)
	m.HandleFunc("GET /api/servers/{name}/terminal", s.terminal)
	m.HandleFunc("GET /api/servers/{name}/icon", s.icon)

	m.HandleFunc("GET /api/servers/{name}/spec", s.getSpec)
	m.HandleFunc("POST /api/servers", s.createServer)
	m.HandleFunc("PUT /api/servers/{name}", s.updateServer)
	m.HandleFunc("DELETE /api/servers/{name}", s.deleteServer)
	m.HandleFunc("POST /api/servers/{name}/enabled", s.setEnabled)

	m.HandleFunc("GET /api/config", s.getConfig)
	m.HandleFunc("PUT /api/config", s.putConfig)
	m.HandleFunc("GET /api/listeners", s.listeners)
	m.HandleFunc("POST /api/listeners/kill", s.killListener)
	m.HandleFunc("GET /api/discover", s.discover)
	m.HandleFunc("GET /api/free-port", s.freePort)
	m.HandleFunc("POST /api/open", s.open)
	m.HandleFunc("GET /api/apps", s.getApps)
	m.HandleFunc("PUT /api/apps", s.setApp)
	m.HandleFunc("POST /api/shutdown", s.shutdown)
	m.HandleFunc("POST /api/upgrade", s.upgrade)
	s.containerRoutes(m)
	s.remoteRoutes(m)

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, errors.New("no such endpoint"))
	})
	m.HandleFunc("/", s.serveUI)
	s.mux = m
}

// ── state ────────────────────────────────────────────────────────────────

// Health identifies the daemon to clients probing the port.
type Health struct {
	App       string    `json:"app"`
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"startedAt"`
	// SampleIntervalMs is the spacing of cpuHistory/memHistory samples.
	SampleIntervalMs int `json:"sampleIntervalMs"`
}

func (s *Server) healthInfo() Health {
	return Health{App: app.ID, Name: app.Name, Version: app.Version, PID: os.Getpid(), Port: s.opt.Port, StartedAt: s.started,
		SampleIntervalMs: int(supervisor.SampleInterval / time.Millisecond)}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.healthInfo())
}

// State is everything the dashboard renders.
type State struct {
	Warnings []string          `json:"warnings,omitempty"`
	Daemon   Health            `json:"daemon"`
	Config   ConfigInfo        `json:"config"`
	Servers  []supervisor.View `json:"servers"`
	Groups   []string          `json:"groups"`
	Docker   docker.State      `json:"docker"`
	// Remote is the connection to another device, null when there is none.
	Remote *remote.Info `json:"remote"`
}

// ConfigInfo describes the loaded configuration file.
type ConfigInfo struct {
	Path        string   `json:"path"`
	PathDisplay string   `json:"pathDisplay"`
	Root        string   `json:"root"`
	Error       string   `json:"error,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	StateDir    string   `json:"stateDir"`
	// Revision changes whenever the file's text changes.
	Revision string `json:"revision"`
	// Notify mirrors the notify setting for clients that notify themselves.
	Notify bool `json:"notify"`
}

func (s *Server) snapshot() State {
	cfg := s.sup.Config()
	views := s.sup.Snapshot(s.opt.Port)
	return State{
		Daemon: s.healthInfo(),
		Config: ConfigInfo{
			Path:        s.opt.ConfigPath,
			PathDisplay: paths.ShortenHome(s.opt.ConfigPath),
			Root:        paths.ShortenHome(cfg.Root),
			Error:       s.configError(),
			Warnings:    cfg.Warnings,
			StateDir:    paths.ShortenHome(s.opt.StateDir),
			Revision:    s.configRevision(),
			Notify:      cfg.Notify,
		},
		Servers: views,
		Groups:  cfg.Groups(),
		Docker:  s.dockerState(views),
		Remote:  s.opt.Remote.Info(),
	}
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot())
}

// events streams the full state as JSON text messages whenever it changes,
// at most four times per second.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	if r.URL.Query().Get("notifier") == "1" {
		defer s.sup.AddDesktopClient()()
	}
	ch := s.sup.Subscribe()
	defer s.sup.Unsubscribe(ch)
	dch := s.opt.Docker.Subscribe()
	defer s.opt.Docker.Unsubscribe(dch)
	rch := s.opt.Remote.SubscribeInfo()
	defer s.opt.Remote.Unsubscribe(rch)
	send := func() error {
		data, err := json.Marshal(s.snapshot())
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, data)
	}
	if send() != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-dch:
		case <-rch:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		if send() != nil {
			return
		}
	}
}

// ── actions ──────────────────────────────────────────────────────────────

type actionBody struct {
	Takeover bool   `json:"takeover"`
	Timeout  string `json:"timeout,omitempty"`
}

func (s *Server) action(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body actionBody
		if err := readJSON(r, &body); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if _, ok := s.sup.Lookup(name); !ok {
			fail(w, http.StatusNotFound, fmt.Errorf("unknown server %q", name))
			return
		}
		wait := time.Duration(0)
		if body.Timeout != "" {
			d, err := time.ParseDuration(body.Timeout)
			if err != nil || d <= 0 {
				fail(w, http.StatusBadRequest, errors.New("timeout must be a positive duration"))
				return
			}
			wait = d
		}
		opt := supervisor.StartOptions{Takeover: body.Takeover}
		var err error
		switch kind {
		case "start":
			err = s.sup.Start(name, opt)
		case "stop":
			err = s.sup.Dismiss(name)
		case "restart":
			err = s.sup.Restart(name, opt)
		}
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if kind == "restart" && wait > 0 {
			if err := s.sup.WaitReady(r.Context(), name, wait); err != nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "server": s.view(name)})
				return
			}
		}
		if v := s.view(name); v != nil {
			writeJSON(w, http.StatusOK, v)
			return
		}
		// A stopped temporary server is gone; report it as stopped.
		writeJSON(w, http.StatusOK, supervisor.View{Name: name, Status: supervisor.Stopped})
	}
}

func (s *Server) view(name string) *supervisor.View {
	for _, v := range s.sup.Snapshot(s.opt.Port) {
		if v.Name == name {
			return &v
		}
	}
	return nil
}

type ensureBody struct {
	Timeout  string `json:"timeout"`
	Takeover bool   `json:"takeover"`
	// Dir is the caller's checkout; another checkout of the same repository
	// gets a temporary copy of the server.
	Dir string `json:"dir,omitempty"`
}

// ensure starts a server if needed and waits until it is ready. Agents use
// it to get a working URL without ever launching a duplicate.
func (s *Server) ensure(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body ensureBody
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if body.Dir != "" {
		target, err := s.sup.ForCheckout(name, body.Dir)
		if err != nil {
			status := http.StatusBadRequest
			if _, ok := s.sup.Config().Server(name); !ok {
				status = http.StatusNotFound
			}
			fail(w, status, err)
			return
		}
		name = target
	}
	timeout := 90 * time.Second
	if body.Timeout != "" {
		d, err := time.ParseDuration(body.Timeout)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		timeout = d
	}
	if err := s.sup.Start(name, supervisor.StartOptions{Takeover: body.Takeover}); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.sup.WaitReady(r.Context(), name, timeout); err != nil {
		v := s.view(name)
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "server": v})
		return
	}
	writeJSON(w, http.StatusOK, s.view(name))
}

type bulkBody struct {
	Action   string   `json:"action"`
	Names    []string `json:"names"` // server or group names
	All      bool     `json:"all"`
	Takeover bool     `json:"takeover"`
}

type bulkResult struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
}

func (s *Server) bulk(w http.ResponseWriter, r *http.Request) {
	var body bulkBody
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.sup.Config()
	var names []string
	if body.All {
		for _, sv := range cfg.Servers {
			if sv.Enabled || body.Action == "stop" {
				names = append(names, sv.Name)
			}
		}
	} else {
		var err error
		if names, err = s.resolve(cfg, body.Names); err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
	}
	res := make([]bulkResult, len(names))
	for i, n := range names {
		res[i].Name = n
	}
	opt := supervisor.StartOptions{Takeover: body.Takeover}
	parallel := func(fn func(string) error) {
		var wg sync.WaitGroup
		for i, n := range names {
			wg.Add(1)
			go func(i int, n string) {
				defer wg.Done()
				if err := fn(n); err != nil && res[i].Error == "" {
					res[i].Error = err.Error()
				}
			}(i, n)
		}
		wg.Wait()
	}
	switch body.Action {
	case "start":
		parallel(func(n string) error { return s.sup.Start(n, opt) })
	case "stop":
		parallel(s.sup.Dismiss)
	case "restart":
		// Stop everything first, then start in dependency order, so a
		// dependent never comes up against a dependency about to go down.
		parallel(s.sup.Stop)
		idx := map[string]int{}
		for i, n := range names {
			idx[n] = i
		}
		for _, n := range cfg.WithDeps(names) {
			i, selected := idx[n]
			if !selected {
				continue
			}
			if err := s.sup.Start(n, opt); err != nil {
				res[i].Error = err.Error()
			}
		}
	default:
		fail(w, http.StatusBadRequest, fmt.Errorf("unknown action %q", body.Action))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// resolve expands server and group names like Config.Resolve, and also
// accepts temporary servers, which come after the configured ones.
func (s *Server) resolve(cfg *config.Config, targets []string) ([]string, error) {
	var configured, temporary []string
	for _, t := range targets {
		if s.sup.IsTemporary(t) {
			if !slices.Contains(temporary, t) {
				temporary = append(temporary, t)
			}
		} else {
			configured = append(configured, t)
		}
	}
	var names []string
	if len(configured) > 0 {
		var err error
		if names, err = cfg.Resolve(configured); err != nil {
			return nil, err
		}
	}
	return append(names, temporary...), nil
}

// ── logs and terminal ────────────────────────────────────────────────────

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	lb, ok := s.sup.Logs(r.PathValue("name"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(lb.Raw())
		return
	}
	q, err := parseLogQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeLines(w, q, lb.TimedLines())
}

func (s *Server) clearLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sup.Logs(r.PathValue("name")); !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	s.sup.ClearLogs(r.PathValue("name"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type termMsg struct {
	T    string `json:"t"` // "i" input, "r" resize
	D    string `json:"d,omitempty"`
	Cols uint16 `json:"c,omitempty"`
	Rows uint16 `json:"r,omitempty"`
}

// terminal streams a server's raw output as binary messages (scrollback
// first) and accepts keystrokes and resize requests as JSON text messages.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	lb, ok := s.sup.Logs(name)
	if !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	readOnly := r.URL.Query().Get("readonly") == "1"
	streamBuffer(w, r, lb, func(m termMsg) {
		switch m.T {
		case "i":
			if !readOnly {
				_ = s.sup.Input(name, []byte(m.D))
			}
		case "r":
			s.sup.Resize(name, m.Cols, m.Rows)
		}
	})
}

// streamBuffer sends a log buffer over a WebSocket: the scrollback, then
// live output, coalescing bursts. Text messages from the client go to input.
func streamBuffer(w http.ResponseWriter, r *http.Request, lb *supervisor.LogBuffer, input func(termMsg)) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 16)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	backlog, ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)
	// With always=1 the scrollback is sent even when empty, so a client can
	// tell it apart from live output.
	if r.URL.Query().Get("tail") != "0" && (len(backlog) > 0 || r.URL.Query().Get("always") == "1") {
		if err := c.Write(ctx, websocket.MessageBinary, backlog); err != nil {
			return
		}
	}
	go readTermMsgs(ctx, cancel, c, input)
	for {
		select {
		case <-ctx.Done():
			return
		case chunk := <-ch:
			buf := chunk
		drain:
			for len(buf) < 256<<10 {
				select {
				case more := <-ch:
					buf = append(buf, more...)
				default:
					break drain
				}
			}
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageBinary, buf)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}

// readTermMsgs decodes client messages until the socket closes, then cancels.
func readTermMsgs(ctx context.Context, cancel context.CancelFunc, c *websocket.Conn, input func(termMsg)) {
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m termMsg
		if json.Unmarshal(data, &m) == nil && input != nil {
			input(m)
		}
	}
}

// iconTypes are the image formats served as project icons, by lowercase extension.
var iconTypes = map[string]string{
	".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif",
}

func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.sup.Lookup(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	p := supervisor.IconPath(sv.Dir, sv.Icon)
	ctype := iconTypes[strings.ToLower(filepath.Ext(p))]
	if p == "" || ctype == "" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) > 2<<20 {
		http.NotFound(w, r)
		return
	}
	// Project files are untrusted: an SVG opened directly must not run script
	// on the dashboard's origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "max-age=300")
	_, _ = w.Write(data)
}

// ── config editing ───────────────────────────────────────────────────────

func (s *Server) getSpec(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.sup.Lookup(r.PathValue("name"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	writeJSON(w, http.StatusOK, config.SpecOf(sv))
}

// edit applies fn to the config file and reloads it. warnings go back with the
// new state; rename (old, new) carries a running server over to its new name.
func (s *Server) edit(w http.ResponseWriter, fn func() error, warnings []string, rename ...string) {
	s.editMu.Lock()
	err := fn()
	if err == nil {
		err = s.reload(rename...)
	}
	s.editMu.Unlock()
	if errors.Is(err, config.ErrConflict) {
		fail(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	state := s.snapshot()
	state.Warnings = warnings
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	var spec config.Spec
	if err := readJSON(r, &spec); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.normalizeSpec(&spec)
	s.edit(w, func() error { return config.AddServer(s.opt.ConfigPath, spec) }, directoryWarnings(s.sup.Config().Root, spec.Dir))
}

// updateServer replaces a server's table. A body that names the config
// revision its form was loaded from is refused with 409 when the file changed
// since, so the edit never overwrites a change the form did not show.
func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		config.Spec
		Revision string `json:"revision"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	spec := body.Spec
	s.normalizeSpec(&spec)
	s.edit(w, func() error {
		if body.Revision != "" {
			if err := config.CheckRevision(s.opt.ConfigPath, body.Revision); err != nil {
				return err
			}
		}
		return config.UpdateServer(s.opt.ConfigPath, r.PathValue("name"), spec)
	}, directoryWarnings(s.sup.Config().Root, spec.Dir), r.PathValue("name"), spec.Name)
}

func directoryWarnings(root, dir string) []string {
	dir = paths.ExpandHome(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	st, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{fmt.Sprintf("directory %s does not exist", paths.ShortenHome(dir))}
	}
	if err == nil && !st.IsDir() {
		return []string{fmt.Sprintf("%s is not a directory", paths.ShortenHome(dir))}
	}
	return nil
}

// normalizeSpec makes directories under root relative and trims input.
func (s *Server) normalizeSpec(sp *config.Spec) {
	sp.Name = strings.TrimSpace(sp.Name)
	sp.Cmd = strings.TrimSpace(sp.Cmd)
	sp.Dir = strings.TrimSpace(sp.Dir)
	sp.Group = strings.TrimSpace(sp.Group)
	root := s.sup.Config().Root
	abs := paths.ExpandHome(sp.Dir)
	if filepath.IsAbs(abs) {
		if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
			sp.Dir = rel
			if rel == "." {
				sp.Dir = ""
			}
		} else {
			sp.Dir = paths.ShortenHome(abs)
		}
	}
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.edit(w, func() error { return config.RemoveServer(s.opt.ConfigPath, name) }, nil)
}

func (s *Server) setEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.edit(w, func() error { return config.SetEnabled(s.opt.ConfigPath, r.PathValue("name"), body.Enabled) }, nil)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.opt.ConfigPath)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": s.opt.ConfigPath, "text": string(data), "revision": config.Revision(data)})
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text     string `json:"text"`
		Revision string `json:"revision"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if body.Revision == "" {
		fail(w, http.StatusBadRequest, errors.New("revision is required; reload the config and try again"))
		return
	}
	s.editMu.Lock()
	// Sharing is controlled on this computer only: compared with the file
	// being replaced, under the edit lock, so no concurrent edit slips in.
	if fromPeer(r) && s.changesShare(body.Text) {
		s.editMu.Unlock()
		fail(w, http.StatusForbidden, errors.New("sharing can only be changed on this computer"))
		return
	}
	err := config.ReplaceIfUnchanged(s.opt.ConfigPath, []byte(body.Text), body.Revision)
	if err == nil {
		err = s.reload()
	}
	s.editMu.Unlock()
	if errors.Is(err, config.ErrConflict) {
		fail(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.snapshot())
}

// changesShare reports whether text sets share differently from the file on
// disk (or, when that does not parse, from the applied config).
func (s *Server) changesShare(text string) bool {
	cur := s.sup.Config().Share
	if old, err := config.Load(s.opt.ConfigPath); err == nil {
		cur = old.Share
	}
	next, err := config.Parse(s.opt.ConfigPath, []byte(text))
	return err == nil && next.Share != cur
}

// ── ports and discovery ──────────────────────────────────────────────────

// ListenerView is a listening process annotated with what Portside knows.
type ListenerView struct {
	ports.Listener
	CwdDisplay string `json:"cwdDisplay,omitempty"`
	Server     string `json:"server,omitempty"`    // configured server owning the port
	Managed    bool   `json:"managed"`             // process started by Portside
	Via        string `json:"via,omitempty"`       // "containers" when a runtime publishes a managed compose port
	Protected  string `json:"protected,omitempty"` // why it may not be killed
	// Container is the container whose published port this is.
	Container *ContainerRef `json:"container,omitempty"`
}

// ContainerRef names a container and the server it belongs to.
type ContainerRef struct {
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
	Server  string `json:"server,omitempty"`
}

func (s *Server) listeners(w http.ResponseWriter, r *http.Request) {
	cfg := s.sup.Config()
	byPort := map[int]string{}
	for _, sv := range cfg.Servers {
		if sv.Port > 0 {
			byPort[sv.Port] = sv.Name
		}
	}
	live := map[string]bool{}
	for _, v := range s.sup.Snapshot(s.opt.Port) {
		if v.PID != 0 {
			live[v.Name] = true
		}
	}
	published := map[int]*ContainerRef{}
	for _, c := range s.dockerState(nil).Containers {
		for _, p := range c.Ports {
			if p.Proto == "tcp" {
				published[p.Public] = &ContainerRef{Name: c.Name, Service: c.Service, Server: c.Server}
			}
		}
	}
	var out []ListenerView
	for _, l := range ports.List(r.Context()) {
		v := ListenerView{Listener: l, CwdDisplay: paths.ShortenHome(l.Cwd), Server: byPort[l.Port]}
		if supervisor.IsContainerRuntime(l.Command) {
			v.Container = published[l.Port]
		}
		if l.PID == os.Getpid() {
			v.Server = app.ID
			v.Managed = true
			v.Protected = "this is " + app.Name + " itself"
		} else if name, ok := s.sup.IsManagedPID(l.PID); ok {
			v.Managed = true
			v.Server = name
		} else if sv, ok := cfg.Server(v.Server); ok && live[sv.Name] && supervisor.IsComposeCommand(sv.Cmd) && supervisor.IsContainerRuntime(l.Command) {
			// A running compose stack: the runtime publishes its containers' ports.
			v.Managed = true
			v.Via = "containers"
			v.Protected = s.sup.Killable(l)
		} else {
			v.Protected = s.sup.Killable(l)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"listeners": out})
}

func (s *Server) killListener(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PID  int `json:"pid"`
		Port int `json:"port"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	// Only processes currently listening may be killed through the API.
	var target *ports.Listener
	for _, l := range ports.List(r.Context()) {
		if l.PID == body.PID && (body.Port == 0 || l.Port == body.Port) {
			l := l
			target = &l
			break
		}
	}
	if target == nil {
		fail(w, http.StatusNotFound, fmt.Errorf("no listening process with pid %d", body.PID))
		return
	}
	if err := s.sup.KillListener(*target); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DiscoverResult is a suggestion with a free port reserved for it.
type DiscoverResult struct {
	discover.Suggestion
	DirDisplay string `json:"dirDisplay"`
	DirConfig  string `json:"dirConfig"` // how dir would be written to the config
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	cfg := s.sup.Config()
	root := cfg.Root
	if q := r.URL.Query().Get("root"); q != "" {
		root = paths.ExpandHome(q)
	}
	out := []DiscoverResult{}
	for _, sg := range discover.Prepare(cfg, discover.Scan(root, 3, nil)) {
		res := DiscoverResult{Suggestion: sg, DirDisplay: paths.ShortenHome(sg.Dir), DirConfig: paths.ShortenHome(sg.Dir)}
		if rel, err := filepath.Rel(cfg.Root, sg.Dir); err == nil && !strings.HasPrefix(rel, "..") {
			res.DirConfig = rel
		}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": paths.ShortenHome(root), "suggestions": out})
}

func (s *Server) freePort(w http.ResponseWriter, r *http.Request) {
	temporary := s.sup.TemporaryPorts()
	p := s.sup.Config().FreePort(func(p int) bool { return temporary(p) || ports.Listening(p) })
	writeJSON(w, http.StatusOK, map[string]int{"port": p})
}

// upgrade replaces the daemon's binary in place with the one at the
// optional {"exe": path}, keeping servers running. On success the process
// becomes the new daemon and this connection drops without a reply; a reply
// means the old daemon still runs, and says why.
func (s *Server) upgrade(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Exe string `json:"exe"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if s.opt.Upgrade == nil {
		fail(w, http.StatusNotImplemented, errors.New("this daemon cannot upgrade in place"))
		return
	}
	if err := s.opt.Upgrade(body.Exe); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// shutdown stops the daemon. The optional body {"restart": true} says a new
// daemon follows; without it the stop is deliberate and stays in effect.
func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Restart bool `json:"restart"`
	}
	_ = readJSON(r, &body)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if s.opt.Shutdown != nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			s.opt.Shutdown(body.Restart)
		}()
	}
}
