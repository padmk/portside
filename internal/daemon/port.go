package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// autoPortFile records the port a daemon whose config sets no port chose
// because the default was taken. It lives in the state directory, never in
// the config: the config stays without `port`, so the port stays automatic,
// and daemons with other state directories that share the config are not
// affected.
func autoPortFile(stateDir string) string { return filepath.Join(stateDir, "auto-port") }

// SavedPort returns the automatically chosen port recorded in stateDir, or 0.
// Only a port from app.FallbackPorts counts.
func SavedPort(stateDir string) int {
	b, err := os.ReadFile(autoPortFile(stateDir))
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if slices.Contains(app.FallbackPorts(), p) {
		return p
	}
	return 0
}

// ErrPortHeld means this product, running for this account with other
// settings, holds the port the daemon would use.
var ErrPortHeld = errors.New("the daemon's port is held by another of its daemons")

// portHeld is an ErrPortHeld that says which daemon holds the port.
type portHeld string

func (e portHeld) Error() string        { return string(e) }
func (e portHeld) Is(target error) bool { return target == ErrPortHeld }

// listenConfigured listens on the configured port. A port the config sets
// is used or the start fails, naming the holder where it can be seen.
//
// Without a configured port the daemon tries the port it chose before
// (SavedPort), then the default, then app.FallbackPorts in order, skipping
// ports that servers use, and records a fallback port in the state
// directory so it stays the same from then on. It never moves away from a
// port that this product holds for this account (a daemon with another
// state directory, say): that would leave two daemons serving one config.
// cfg.Port is updated; clients follow through daemon.port.
func listenConfigured(cfg *config.Config, cfgPath, stateDir string, avoid func(port int) bool) ([]net.Listener, error) {
	if !cfg.PortAuto {
		lns, err := listen(cfg.Port)
		if _, ok := errors.AsType[*busyError](err); ok {
			return nil, fmt.Errorf("%w; %s, or change `port` in %s", err, holder(cfg.Port), paths.ShortenHome(cfgPath))
		}
		return lns, err
	}
	used := func(port int) bool {
		if config.Reserved[port] || (avoid != nil && avoid(port)) {
			return true
		}
		for _, sv := range cfg.Servers {
			if sv.Port == port {
				return true
			}
		}
		return false
	}
	var cands []int
	if p := SavedPort(stateDir); p > 0 {
		cands = append(cands, p)
	}
	cands = append(cands, app.DefaultPort)
	for _, p := range app.FallbackPorts() {
		if len(cands) == 0 || p != cands[0] {
			cands = append(cands, p)
		}
	}
	want := cands[0]
	for i, p := range cands {
		if p != app.DefaultPort && used(p) {
			continue
		}
		lns, err := listen(p)
		var busy *busyError
		if err != nil && !errors.As(err, &busy) {
			return nil, err
		}
		if err != nil {
			// The port this daemon would use, or the default, is held by
			// this product for this account: report it instead of moving.
			if i == 0 || p == app.DefaultPort {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				h := client.PortHolder(ctx, p)
				cancel()
				if h.Ours() {
					return nil, portHeld(h.Refusal(p))
				}
			}
			continue
		}
		cfg.Port = p
		if p != want {
			log.Printf("port %d is taken (%s); using %d", want, holder(want), p)
		}
		if p == app.DefaultPort {
			_ = os.Remove(autoPortFile(stateDir))
		} else if p != SavedPort(stateDir) {
			if err := paths.WriteFile(autoPortFile(stateDir), []byte(strconv.Itoa(p)+"\n")); err != nil {
				log.Printf("saving port %d: %v", p, err)
			} else {
				log.Printf("port %d is kept in %s while the config sets no `port`", p, paths.ShortenHome(autoPortFile(stateDir)))
			}
		}
		return lns, nil
	}
	return nil, fmt.Errorf("port %d and the fallback ports %d-%d and %d-%d are all taken; set `port` in %s",
		app.DefaultPort, app.DefaultPort+1, app.DefaultPort+21, app.DefaultPort+10001, app.DefaultPort+10021, paths.ShortenHome(cfgPath))
}

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

// listen binds IPv4 and IPv6 loopback. Both are required where the system
// has IPv6 loopback: browsers resolve *.localhost to ::1 first, so a
// program of another account holding [::1]:<port> would get every
// dashboard request and its cookie. A taken address is a *busyError.
//
// Anything that accepts connections on the port's loopback addresses counts
// as taken before binding: macOS lets 127.0.0.1:<port> be bound while
// another program listens on the wildcard address of that port, and
// connections would then reach either.
func listen(port int) ([]net.Listener, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for _, host := range []string{"127.0.0.1", "::1"} {
		a := net.JoinHostPort(host, strconv.Itoa(port))
		if c, err := net.DialTimeout("tcp", a, 250*time.Millisecond); err == nil {
			c.Close()
			return nil, &busyError{addr: a, v6: host == "::1"}
		}
	}
	v4, err := net.Listen("tcp", addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, &busyError{addr: addr}
	} else if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w (change `port` in the config)", addr, err)
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
