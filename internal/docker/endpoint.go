// Package docker talks to a container engine through the Docker Engine API.
// It works with any engine that serves that API on the active Docker context:
// Docker Desktop, OrbStack, Colima, Rancher Desktop (dockerd), Podman or a
// plain Linux daemon, rootful or rootless, on a socket, over TCP with or
// without TLS, or over SSH.
package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Endpoint is where the engine API is served.
type Endpoint struct {
	// Network is "unix", "tcp" or "ssh"; Address is the socket path, the
	// host:port, or for SSH the host name.
	Network string `json:"-"`
	Address string `json:"-"`
	// Host is the endpoint as written in the context, e.g. unix:///var/run/docker.sock.
	Host string `json:"host"`
	// Context is the Docker context it came from, "" for DOCKER_HOST or a guess.
	Context string `json:"context,omitempty"`
	// Target is where a socket symlink finally points, which names the
	// engine behind /var/run/docker.sock.
	Target string `json:"-"`

	// TLS settings for tcp://: certificates in CertDir (ca.pem, cert.pem,
	// key.pem, each optional), and whether the server certificate is checked.
	TLS       bool   `json:"-"`
	TLSVerify bool   `json:"-"`
	CertDir   string `json:"-"`

	// SSH settings for ssh://user@host:port.
	SSHUser string `json:"-"`
	SSHPort string `json:"-"`

	// defaultPort marks a tcp:// host written without a port.
	defaultPort bool
}

// useTLS turns on TLS for a tcp:// endpoint, which then defaults to port 2376.
func (ep *Endpoint) useTLS(verify bool, certDir string) {
	ep.TLS, ep.TLSVerify, ep.CertDir = true, verify, certDir
	if ep.defaultPort {
		host, _, _ := net.SplitHostPort(ep.Address)
		ep.Address = net.JoinHostPort(host, "2376")
	}
}

// Resolve finds the active engine endpoint the way the docker CLI does:
// DOCKER_HOST, then DOCKER_CONTEXT, then the current context in the CLI
// config, then the first engine socket found. getenv reads the user's
// environment.
func Resolve(getenv func(string) string) (Endpoint, error) {
	cfgDir := getenv("DOCKER_CONFIG")
	if cfgDir == "" {
		home, _ := os.UserHomeDir()
		cfgDir = filepath.Join(home, ".docker")
	}
	if h := getenv("DOCKER_HOST"); h != "" {
		ep, err := parseHost(h, "")
		if err != nil {
			return ep, err
		}
		if ep.Network == "tcp" && (getenv("DOCKER_TLS_VERIFY") != "" || getenv("DOCKER_TLS") != "") {
			certDir := getenv("DOCKER_CERT_PATH")
			if certDir == "" {
				certDir = cfgDir
			}
			ep.useTLS(getenv("DOCKER_TLS_VERIFY") != "", certDir)
		}
		return ep, nil
	}
	ctx := getenv("DOCKER_CONTEXT")
	if ctx == "" {
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if data, err := os.ReadFile(filepath.Join(cfgDir, "config.json")); err == nil {
			_ = json.Unmarshal(data, &cfg)
		}
		ctx = cfg.CurrentContext
	}
	if ctx != "" && ctx != "default" {
		sum := sha256.Sum256([]byte(ctx))
		id := hex.EncodeToString(sum[:])
		meta := filepath.Join(cfgDir, "contexts", "meta", id, "meta.json")
		var m struct {
			Endpoints map[string]struct {
				Host          string `json:"Host"`
				SkipTLSVerify bool   `json:"SkipTLSVerify"`
			} `json:"Endpoints"`
		}
		data, err := os.ReadFile(meta)
		if err != nil {
			return Endpoint{}, errors.New("docker context " + ctx + " not found")
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return Endpoint{}, errors.New("docker context " + ctx + ": " + err.Error())
		}
		d := m.Endpoints["docker"]
		ep, err := parseHost(d.Host, ctx)
		if err != nil {
			return ep, err
		}
		if ep.Network == "tcp" {
			tlsDir := filepath.Join(cfgDir, "contexts", "tls", id, "docker")
			if _, err := os.Stat(tlsDir); err == nil || d.SkipTLSVerify {
				ep.useTLS(!d.SkipTLSVerify, tlsDir)
			}
		}
		return ep, nil
	}
	return defaultEndpoint(getenv), nil
}

// socketCandidates lists where engines put their API socket, in the order
// they are tried when no context or DOCKER_HOST names one.
func socketCandidates(getenv func(string) string) []string {
	home, _ := os.UserHomeDir()
	in := func(rel ...string) string { return filepath.Join(append([]string{home}, rel...)...) }
	list := []string{"/var/run/docker.sock"}
	if runtime.GOOS == "darwin" {
		list = append(list,
			in(".orbstack", "run", "docker.sock"),
			in(".docker", "run", "docker.sock"),
			in(".colima", "default", "docker.sock"),
			in(".rd", "docker.sock"),
		)
		// Podman machines: version 5 keeps the API socket in TMPDIR,
		// version 4 below its data directory.
		if tmp := getenv("TMPDIR"); tmp != "" {
			m, _ := filepath.Glob(filepath.Join(tmp, "podman", "*-api.sock"))
			list = append(list, m...)
		}
		for _, pattern := range []string{"*/podman.sock", "*/*/podman.sock"} {
			m, _ := filepath.Glob(in(".local", "share", "containers", "podman", "machine", pattern))
			list = append(list, m...)
		}
		return list
	}
	run := getenv("XDG_RUNTIME_DIR")
	if run == "" {
		run = "/run/user/" + strconv.Itoa(os.Getuid())
	}
	return append(list,
		filepath.Join(run, "docker.sock"),
		in(".docker", "desktop", "docker.sock"),
		filepath.Join(run, "podman", "podman.sock"),
		"/run/podman/podman.sock",
	)
}

// defaultEndpoint is the first engine socket that exists, else the system
// socket, which then reports that Docker is not running.
func defaultEndpoint(getenv func(string) string) Endpoint {
	list := socketCandidates(getenv)
	p := list[0]
	for _, c := range list {
		if _, err := os.Stat(c); err == nil {
			p = c
			break
		}
	}
	return Endpoint{Network: "unix", Address: p, Host: "unix://" + p, Target: linkTarget(p)}
}

// linkTarget follows a chain of symlinks, also a dangling one, and returns
// the last path, or "" when p is not a symlink.
func linkTarget(p string) string {
	target := ""
	for range 8 {
		next, err := os.Readlink(p)
		if err != nil {
			break
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(p), next)
		}
		target, p = next, next
	}
	return target
}

func parseHost(h, ctx string) (Endpoint, error) {
	switch {
	case strings.HasPrefix(h, "unix://"):
		p := strings.TrimPrefix(h, "unix://")
		return Endpoint{Network: "unix", Address: p, Host: h, Context: ctx, Target: linkTarget(p)}, nil
	case strings.HasPrefix(h, "tcp://"):
		addr := strings.TrimSuffix(strings.TrimPrefix(h, "tcp://"), "/")
		ep := Endpoint{Network: "tcp", Address: addr, Host: h, Context: ctx}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			// The docker CLI's default ports: 2375, or 2376 with TLS.
			ep.Address = net.JoinHostPort(strings.Trim(addr, "[]"), "2375")
			ep.defaultPort = true
		}
		return ep, nil
	case strings.HasPrefix(h, "ssh://"):
		u, err := url.Parse(h)
		if err != nil || u.Hostname() == "" {
			return Endpoint{}, errors.New("invalid docker endpoint " + h)
		}
		return Endpoint{Network: "ssh", Address: u.Hostname(), Host: h, Context: ctx, SSHUser: u.User.Username(), SSHPort: u.Port()}, nil
	case h == "":
		return Endpoint{}, errors.New("docker context " + ctx + " has no engine endpoint")
	}
	return Endpoint{}, errors.New("unsupported docker endpoint " + h + " (use unix://, tcp:// or ssh://)")
}

// cliEnv is the environment that points the docker CLI at this endpoint
// with the same TLS settings.
func (ep Endpoint) cliEnv() []string {
	env := []string{"DOCKER_HOST=" + ep.Host}
	if ep.TLS {
		env = append(env, "DOCKER_CERT_PATH="+ep.CertDir)
		if ep.TLSVerify {
			env = append(env, "DOCKER_TLS_VERIFY=1")
		} else {
			env = append(env, "DOCKER_TLS=1")
		}
	}
	return env
}
