// Package daemon runs the long-lived process: it owns the supervisor and
// serves the HTTP API, dashboard and proxy until it receives a signal.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/agentskill"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tailnet"
	"github.com/padmk/portside/web"
)

// ErrAlreadyRunning means another daemon holds the state directory's lock.
var ErrAlreadyRunning = errors.New("another daemon is already running")

// MarkStopped records a deliberate stop, so background clients do not start
// a daemon again until something starts one explicitly.
func MarkStopped() error {
	if err := os.MkdirAll(paths.StateDir(), paths.DirMode); err != nil {
		return err
	}
	return os.WriteFile(paths.StopMarker(), []byte(time.Now().Format(time.RFC3339)+"\n"), paths.FileMode)
}

// ClearStopped removes the record of a deliberate stop.
func ClearStopped() { _ = os.Remove(paths.StopMarker()) }

// Stopped reports whether the daemon was stopped deliberately and none started since.
func Stopped() bool {
	_, err := os.Stat(paths.StopMarker())
	return err == nil
}

// Run blocks until the daemon is asked to stop. It returns ErrAlreadyRunning
// when another daemon owns the state directory.
func Run(cfgPath string) error {
	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, paths.DirMode); err != nil {
		return err
	}
	// A daemon that re-executed itself to upgrade left its lock, sockets
	// and servers to this process.
	ho := takeHandoff(stateDir)
	lockPath := filepath.Join(stateDir, "daemon.lock")
	var lock *os.File
	if ho != nil {
		var err error
		if lock, err = inheritLock(ho.Lock, lockPath); err != nil {
			log.Printf("handoff: lock: %v", err)
		}
	}
	if lock == nil {
		var err error
		if lock, err = acquireLock(lockPath); err != nil {
			return err
		}
	}
	defer lock.Close()
	// Whoever started this daemon wants it running, so the stop is over;
	// but a stop requested while the previous binary handed over wins.
	stopDuringUpgrade := ho != nil && Stopped()
	if !stopDuringUpgrade {
		ClearStopped()
	}

	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := config.WriteStarter(cfgPath); err != nil {
			return err
		}
		log.Printf("created %s", cfgPath)
	}
	// Files left by older versions or created by other tools (the service's
	// log redirect) lose group and other access.
	paths.Restrict(cfgPath, stateDir)
	// Save automatic ports first; an invalid file is reported by LoadRecoverable.
	_, portsErr := config.LoadAssignedPorts(cfgPath, supervisor.SavedTemporaryPorts(stateDir))
	cfg, configErr := config.LoadRecoverable(cfgPath, stateDir)
	if configErr != nil {
		log.Printf("config: %v; using recovered settings", configErr)
	} else if portsErr != nil {
		log.Printf("config: saving automatic ports: %v", portsErr)
	}
	for _, w := range cfg.Warnings {
		log.Printf("config: %s", w)
	}

	// The sockets of an upgrade, when they all came over, else new ones.
	var listeners []net.Listener
	inherited := false
	if ho != nil {
		listeners = inheritListeners(ho.Listeners, cfg.Port)
		if listeners != nil {
			n := len(listeners)
			var err error
			if listeners, err = completeListeners(listeners, cfg.Port); err != nil {
				return err
			}
			inherited = len(listeners) == n
		}
	}
	if listeners == nil {
		var err error
		if listeners, err = listenConfigured(cfg, cfgPath, configErr == nil, supervisor.SavedTemporaryPorts(stateDir)); err != nil {
			return err
		}
	}

	// The dashboard host is new on every cold start, so a page another
	// program served on the old one while the port was free (a service
	// worker, say) has no hold on it. An upgrade that kept all its sockets
	// never released the port, so it keeps the host and its sessions; one
	// that had to listen anew gets a new host as well.
	keepHost := ""
	if ho != nil && inherited {
		keepHost = ho.DashboardHost
	} else if ho != nil {
		log.Printf("handoff: listening anew, so the dashboard gets a new address")
	}
	creds, err := server.LoadCredentials(stateDir, keepHost)
	if err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	sup := supervisor.New(cfg, stateDir)
	dock := docker.NewMonitor(sup, supervisor.SampleInterval, func() string { return sup.Config().DockerStart })
	dock.UseStateDir(stateDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var once sync.Once
	stop := make(chan struct{})
	shutdown := func() { once.Do(func() { close(stop) }) }
	stopping := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	// A stop requested over the API is deliberate unless a restart follows.
	// The marker is written before the listeners close, so a client that
	// finds the daemon gone also finds the marker.
	requestShutdown := func(restart bool) {
		if !restart {
			if err := MarkStopped(); err != nil {
				log.Printf("stop marker: %v", err)
			}
		}
		shutdown()
	}

	ts := tailnet.NewCache(time.Minute)
	rem := remote.New(cfg.Port, stateDir, ts)
	sup.SetPortRelease(rem.Release)
	sup.SetForwarded(rem.Forwarded)
	rem.SetLocalWant(sup.WantsPort)
	sh := share.New(cfg.Port, stateDir, ts)

	upgrades := make(chan upgradeRequest)
	// upgrade checks the new binary, then has the main loop exec it. It
	// returns only when the exec did not happen.
	upgrade := func(exe string) error {
		if exe == "" {
			var err error
			if exe, err = selfExecutable(); err != nil {
				return err
			}
		}
		// A compose job's output pipe would not survive the exec.
		if jobs := dock.RunningJobs(); len(jobs) > 0 {
			return fmt.Errorf("a compose job runs for %s; try again once it has finished", strings.Join(jobs, ", "))
		}
		version, err := checkBinary(exe)
		if err != nil {
			return err
		}
		if stopping() {
			return errors.New("the daemon is shutting down")
		}
		req := upgradeRequest{exe: exe, version: version, result: make(chan error, 1)}
		select {
		case upgrades <- req:
		case <-stop:
			return errors.New("the daemon is shutting down")
		}
		return <-req.result
	}

	srv := server.New(server.Options{
		Supervisor:    sup,
		ConfigError:   errorText(configErr),
		Docker:        dock,
		ConfigPath:    cfgPath,
		StateDir:      stateDir,
		Port:          cfg.Port,
		UI:            web.Assets(),
		Token:         creds.Token,
		DashboardHost: creds.Host,
		Shutdown:      requestShutdown,
		Upgrade:       upgrade,
		Remote:        rem,
		Share:         sh,
	})
	sh.SetHandler(srv.PeerHandler())

	if ho != nil {
		adopted := sup.Adopt(ho.Servers)
		log.Printf("upgraded from %s to %s; kept %d of %d running servers", ho.Version, app.Version, len(adopted), len(ho.Servers))
	}
	// Recover before serving: a request handled earlier could overwrite the
	// state file that records the previous run's process groups.
	sup.Recover(!stopDuringUpgrade)
	if stopDuringUpgrade {
		log.Printf("a stop was requested during the upgrade")
		shutdown()
	}
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, ConnContext: server.ConnContext}
	// serve runs the API on lns. Closing them for an upgrade sets the
	// returned flag first, so that is not taken for a failure.
	serve := func(lns []net.Listener) *atomic.Bool {
		var handedOff atomic.Bool
		for _, ln := range lns {
			go func(ln net.Listener) {
				if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !handedOff.Load() {
					log.Printf("http: %v", err)
					shutdown()
				}
			}(ln)
		}
		return &handedOff
	}
	handedOff := serve(listeners)
	log.Printf("%s %s listening on http://127.0.0.1:%d (pid %d)", app.Name, app.Version, cfg.Port, os.Getpid())
	// Clients find a daemon whose config has since moved to another port here.
	portFile := filepath.Join(stateDir, "daemon.port")
	_ = os.WriteFile(portFile, []byte(strconv.Itoa(cfg.Port)+"\n"), paths.FileMode)
	defer os.Remove(portFile)
	// Keep an installed agent skill in step with this binary. Instances with
	// their own config or state (tests, development) leave it alone. It runs
	// in the background because locating the agents may read the login shell.
	if refreshInstalledSkill(cfgPath) {
		go func(port int) {
			if err := agentskill.Refresh(port, cfgPath); err != nil {
				log.Printf("agent skill: %v", err)
			}
		}(cfg.Port)
	}

	go sup.Run(ctx)
	go srv.WatchContainerIssues(ctx)
	go dock.Run(ctx)
	go srv.WatchConfig(ctx)
	// Sharing is applied in the background (it runs the tailscale CLI); with
	// it off this also withdraws a Serve entry left by a crashed daemon.
	go func() {
		if st := sh.Apply(ctx); st.Error != "" {
			log.Printf("share: %s", st.Error)
		} else if st.Active {
			log.Printf("shared with your other devices at %s", st.URL)
		}
		sh.Run(ctx)
	}()
	// The computer this daemon was connected to when it stopped.
	go rem.Resume(ctx)

	// reexec replaces this process with req.exe and hands the servers over.
	// It returns only when the exec failed, with everything running again.
	reexec := func(req upgradeRequest) error {
		nonce, err := newNonce()
		if err != nil {
			return err
		}
		files, err := listenerFiles(listeners)
		if err != nil {
			return fmt.Errorf("listening sockets: %w", err)
		}
		servers, err := sup.PrepareHandoff()
		if err != nil {
			closeFiles(files)
			return err
		}
		hoPath := handoffPath(stateDir)
		// A descriptor kept open across the exec would leak into any process
		// started meanwhile (a server, lsof, git, an editor), so nothing
		// starts from the first such descriptor until the exec or until
		// all of them close on exec again.
		syscall.ForkLock.Lock()
		forkLocked := true
		closeOnExec := func() {
			if !forkLocked {
				return
			}
			_, _ = keepOnExec(files, false)
			_, _ = keepOnExec([]*os.File{lock}, false)
			_ = supervisor.KeepTerminalsOnExec(servers, false)
			syscall.ForkLock.Unlock()
			forkLocked = false
		}
		fail := func(err error) error {
			closeOnExec()
			sup.CancelHandoff()
			_ = os.Remove(hoPath)
			closeFiles(files)
			return err
		}
		fds, err := keepOnExec(files, true)
		if err != nil {
			return fail(fmt.Errorf("listening sockets: %w", err))
		}
		lockFD, err := supervisor.KeepOnExec(lock, true)
		if err != nil {
			return fail(err)
		}
		if err := supervisor.KeepTerminalsOnExec(servers, true); err != nil {
			return fail(err)
		}
		h := handoff{Nonce: nonce, PID: os.Getpid(), Version: app.Version,
			Lock: lockFD, Port: cfg.Port, Listeners: fds, Servers: servers, DashboardHost: creds.Host}
		if err := writeHandoff(hoPath, h); err != nil {
			return fail(err)
		}
		// A stop requested from here on finds its marker in the new daemon.
		if stopping() {
			return fail(errors.New("the daemon is shutting down"))
		}
		// The sockets stay open in files: connections wait in the backlog
		// until the new binary accepts them.
		handedOff.Store(true)
		for _, ln := range listeners {
			ln.Close()
		}
		log.Printf("upgrading to %s %s (%s); %d running servers keep running", app.Name, req.version, req.exe, len(servers))
		env := append(withoutEnv(os.Environ(), handoffEnv), handoffEnv+"="+nonce)
		execErr := syscall.Exec(req.exe, append([]string{req.exe}, os.Args[1:]...), env)
		// Still this binary: serve again on the same sockets.
		closeOnExec()
		var lns []net.Listener
		for _, f := range files {
			if ln, err := net.FileListener(f); err == nil {
				lns = append(lns, ln)
			}
		}
		if len(lns) == 0 {
			if lns, err = listen(cfg.Port); err != nil {
				log.Printf("upgrade: %v", err)
				shutdown()
			}
		}
		listeners = lns
		handedOff = serve(lns)
		return fail(fmt.Errorf("could not run %s: %w", req.exe, execErr))
	}

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
wait:
	for {
		select {
		case s := <-sig:
			log.Printf("received %s, stopping servers", s)
			break wait
		case <-stop:
			log.Printf("shutdown requested, stopping servers")
			break wait
		case req := <-upgrades:
			// A stop requested at the same time wins.
			if stopping() {
				req.result <- errors.New("the daemon is shutting down")
				log.Printf("shutdown requested, stopping servers")
				break wait
			}
			err := reexec(req)
			log.Printf("upgrade: %v; still running %s", err, app.Version)
			req.result <- err
		}
	}
	// A second signal skips the graceful wait.
	go func() {
		<-sig
		log.Printf("forced exit")
		os.Exit(1)
	}()
	cancel()
	rem.Close()
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	sh.Close(cctx)
	ccancel()
	sup.Shutdown()
	sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer scancel()
	_ = httpSrv.Shutdown(sctx)
	log.Printf("bye")
	return nil
}

// listenConfigured listens on the configured port. When the config sets no
// port and the default is taken (by another program, or by another
// account's daemon on the same computer), it listens on the first free port
// of a fixed range after the default, else on any free port, and saves that
// port in the config when the file is valid, so it stays the same from then
// on. cfg.Port is updated; clients follow through daemon.port. A taken port
// that the config sets is an error naming the holder where it can be seen.
func listenConfigured(cfg *config.Config, cfgPath string, save bool, avoid func(port int) bool) ([]net.Listener, error) {
	lns, err := listen(cfg.Port)
	var busy *busyError
	if err == nil || !errors.As(err, &busy) {
		return lns, err
	}
	if !cfg.PortAuto {
		return nil, fmt.Errorf("%w; %s, or change `port` in %s", err, holder(cfg.Port), paths.ShortenHome(cfgPath))
	}
	used := func(port int) bool {
		if port == cfg.Port || config.Reserved[port] || (avoid != nil && avoid(port)) {
			return true
		}
		for _, sv := range cfg.Servers {
			if sv.Port == port {
				return true
			}
		}
		return false
	}
	taken := cfg.Port
	port := 0
	for p := app.DefaultPort + 1; p <= app.DefaultPort+fallbackPorts; p++ {
		if used(p) {
			continue
		}
		if lns, err = listen(p); err == nil {
			port = p
			break
		}
	}
	for tries := 0; port == 0 && tries < 20; tries++ {
		if lns, err = listen(0); err != nil {
			continue
		}
		if p := lns[0].Addr().(*net.TCPAddr).Port; !used(p) {
			port = p
			break
		}
		closeListeners(lns)
	}
	if port == 0 {
		return nil, fmt.Errorf("%w, and no other free port was found", busy)
	}
	cfg.Port = port
	cfg.PortAuto = false
	log.Printf("port %d is taken (%s); using %d", taken, holder(taken), port)
	if !save {
		log.Printf("config: not saving port %d while the config does not load", port)
		return lns, nil
	}
	note := fmt.Sprintf("%d  # chosen because %d was taken", port, taken)
	if err := config.SetTopLevel(cfgPath, "port", note); err != nil {
		log.Printf("config: saving port %d: %v", port, err)
	} else {
		log.Printf("saved port = %d in %s", port, paths.ShortenHome(cfgPath))
	}
	return lns, nil
}

// fallbackPorts is how many ports after the default are tried, in order,
// before any free port.
const fallbackPorts = 21

// busyError is a loopback address that another program listens on.
type busyError struct {
	addr string
	v6   bool
}

func (e *busyError) Error() string {
	if e.v6 {
		return fmt.Sprintf("cannot listen on %s: another program holds it, and browsers send dashboard requests there first", e.addr)
	}
	return fmt.Sprintf("cannot listen on %s: another program holds it", e.addr)
}

// holder names what listens on port on loopback, or how to find it. A
// process of another account is often invisible to lsof.
func holder(port int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if l, ok := ports.Owner(ctx, port); ok && l.PID > 0 {
		if l.Command != "" {
			return fmt.Sprintf("held by %s, pid %d", l.Command, l.PID)
		}
		return fmt.Sprintf("held by pid %d", l.PID)
	}
	return fmt.Sprintf("find the holder with `lsof -nP -iTCP:%d -sTCP:LISTEN` (a process of another account needs sudo)", port)
}

// listen binds IPv4 and IPv6 loopback; port 0 picks a free port. Both are
// required where the system has IPv6 loopback: browsers resolve *.localhost
// to ::1 first, so a program of another account holding [::1]:<port> would
// get every dashboard request and its cookie. A taken address is a
// *busyError.
func listen(port int) ([]net.Listener, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	v4, err := net.Listen("tcp", addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, &busyError{addr: addr}
	} else if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w (change `port` in the config)", addr, err)
	}
	if port == 0 {
		port = v4.Addr().(*net.TCPAddr).Port
	}
	return completeListeners([]net.Listener{v4}, port)
}

// completeListeners adds an IPv6 loopback listener to lns when they have
// none and the system has IPv6 loopback. Any failure other than a missing
// IPv6 loopback, such as another program holding the address (a
// *busyError), is an error; lns are closed then.
func completeListeners(lns []net.Listener, port int) ([]net.Listener, error) {
	for _, ln := range lns {
		if a, ok := ln.Addr().(*net.TCPAddr); ok && a.IP.Equal(net.IPv6loopback) {
			return lns, nil
		}
	}
	addr := net.JoinHostPort("::1", strconv.Itoa(port))
	v6, err := net.Listen("tcp", addr)
	if err == nil {
		return append(lns, v6), nil
	}
	if noIPv6Loopback(err) {
		return lns, nil
	}
	closeListeners(lns)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, &busyError{addr: addr, v6: true}
	}
	return nil, fmt.Errorf("cannot listen on %s: %w (change `port` in the config)", addr, err)
}

func closeListeners(lns []net.Listener) {
	for _, ln := range lns {
		ln.Close()
	}
}

// noIPv6Loopback reports whether a bind to ::1 failed because the system
// has no IPv6 loopback, rather than because the address is taken.
func noIPv6Loopback(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT)
}

// acquireLock guarantees a single daemon per state directory.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, paths.FileMode)
	if err != nil {
		return nil, err
	}
	// Clients test the lock with a brief shared hold (client.DaemonRunning),
	// so a moment's contention is retried before giving up.
	for tries := 0; ; tries++ {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if tries == 5 {
			f.Close()
			return nil, fmt.Errorf("%w (lock %s)", ErrAlreadyRunning, paths.ShortenHome(path))
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return f, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// refreshInstalledSkill reports whether this daemon is the account's main
// one, with the default config and state, which the installed skill names.
func refreshInstalledSkill(cfgPath string) bool {
	return paths.Default(cfgPath, paths.StateDir())
}
