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
	"sync"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/agentskill"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tailnet"
	"github.com/padmk/portside/web"
)

// Run blocks until the daemon is asked to stop.
func Run(cfgPath string) error {
	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	lock, err := acquireLock(filepath.Join(stateDir, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()

	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := config.WriteStarter(cfgPath); err != nil {
			return err
		}
		log.Printf("created %s", cfgPath)
	}
	// Save automatic ports first; an invalid file is reported by LoadRecoverable.
	_, _ = config.LoadAssignedPorts(cfgPath)
	cfg, configErr := config.LoadRecoverable(cfgPath, stateDir)
	if configErr != nil {
		log.Printf("config: %v; using recovered settings", configErr)
	}
	for _, w := range cfg.Warnings {
		log.Printf("config: %s", w)
	}

	token, err := server.LoadOrCreateToken(stateDir)
	if err != nil {
		return fmt.Errorf("auth token: %w", err)
	}
	sup := supervisor.New(cfg, stateDir)
	dock := docker.NewMonitor(sup, supervisor.SampleInterval, func() string { return sup.Config().DockerStart })
	if err := dock.UseStateDir(stateDir); err != nil {
		return fmt.Errorf("container stop state: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var once sync.Once
	stop := make(chan struct{})
	shutdown := func() { once.Do(func() { close(stop) }) }

	ts := tailnet.NewCache(time.Minute)
	rem := remote.New(cfg.Port, stateDir, ts)
	sup.SetPortRelease(rem.Release)
	sup.SetForwarded(rem.Forwarded)
	sh := share.New(cfg.Port, stateDir, ts)

	srv := server.New(server.Options{
		Supervisor:  sup,
		ConfigError: errorText(configErr),
		Docker:      dock,
		ConfigPath:  cfgPath,
		StateDir:    stateDir,
		Port:        cfg.Port,
		UI:          web.Assets(),
		Token:       token,
		Shutdown:    shutdown,
		Remote:      rem,
		Share:       sh,
	})
	sh.SetHandler(srv.PeerHandler())

	listeners, err := listen(cfg.Port)
	if err != nil {
		return err
	}
	// Recover before serving: a request handled earlier could overwrite the
	// state file that records the previous run's process groups.
	sup.Recover()
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	for _, ln := range listeners {
		go func(ln net.Listener) {
			if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("http: %v", err)
				shutdown()
			}
		}(ln)
	}
	log.Printf("%s %s listening on http://127.0.0.1:%d (pid %d)", app.Name, app.Version, cfg.Port, os.Getpid())
	// Clients find a daemon whose config has since moved to another port here.
	portFile := filepath.Join(stateDir, "daemon.port")
	_ = os.WriteFile(portFile, []byte(strconv.Itoa(cfg.Port)+"\n"), 0o644)
	defer os.Remove(portFile)
	// Keep an installed agent skill in step with this binary. Instances with
	// their own config or state (tests, development) leave it alone.
	if refreshInstalledSkill(cfgPath) {
		if err := agentskill.Refresh(cfg.Port, cfgPath); err != nil {
			log.Printf("agent skill: %v", err)
		}
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

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case s := <-sig:
		log.Printf("received %s, stopping servers", s)
	case <-stop:
		log.Printf("shutdown requested, stopping servers")
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

// listen binds IPv4 loopback (required) and IPv6 loopback (best effort), so
// browsers that resolve *.localhost to ::1 reach the proxy too.
func listen(port int) ([]net.Listener, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	v4, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w (change `port` in the config)", addr, err)
	}
	out := []net.Listener{v4}
	if v6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port))); err == nil {
		out = append(out, v6)
	}
	return out, nil
}

// acquireLock guarantees a single daemon per state directory.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another %s daemon is already running (lock %s)", app.Name, paths.ShortenHome(path))
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

func refreshInstalledSkill(cfgPath string) bool {
	if os.Getenv(app.Env("STATE_DIR")) != "" {
		return false
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		base = filepath.Join(home, ".config")
	}
	actual, err := filepath.Abs(paths.ExpandHome(cfgPath))
	if err != nil {
		return false
	}
	return actual == filepath.Join(base, app.ID, app.ID+".toml")
}
