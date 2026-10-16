// Package docker talks to a container engine through the Docker Engine API.
// It works with any engine that serves that API on the active Docker context:
// Docker Desktop, OrbStack, Colima, Rancher Desktop or a plain Linux daemon.
package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Endpoint is where the engine API is served.
type Endpoint struct {
	// Network is "unix" or "tcp"; Address is the socket path or host:port.
	Network string `json:"-"`
	Address string `json:"-"`
	// Host is the endpoint as written in the context, e.g. unix:///var/run/docker.sock.
	Host string `json:"host"`
	// Context is the Docker context it came from, "" for DOCKER_HOST or a guess.
	Context string `json:"context,omitempty"`
}

// Resolve finds the active engine endpoint the way the docker CLI does:
// DOCKER_HOST, then DOCKER_CONTEXT, then the current context in the CLI
// config, then the platform default. getenv reads the user's environment.
func Resolve(getenv func(string) string) (Endpoint, error) {
	if h := getenv("DOCKER_HOST"); h != "" {
		return parseHost(h, "")
	}
	ctx := getenv("DOCKER_CONTEXT")
	cfgDir := getenv("DOCKER_CONFIG")
	if cfgDir == "" {
		home, _ := os.UserHomeDir()
		cfgDir = filepath.Join(home, ".docker")
	}
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
		meta := filepath.Join(cfgDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
		var m struct {
			Endpoints map[string]struct {
				Host string `json:"Host"`
			} `json:"Endpoints"`
		}
		data, err := os.ReadFile(meta)
		if err != nil {
			return Endpoint{}, errors.New("docker context " + ctx + " not found")
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return Endpoint{}, errors.New("docker context " + ctx + ": " + err.Error())
		}
		return parseHost(m.Endpoints["docker"].Host, ctx)
	}
	return defaultEndpoint(), nil
}

// defaultEndpoint is the system socket, or on macOS the first socket a known
// engine created when the system one is missing.
func defaultEndpoint() Endpoint {
	const system = "/var/run/docker.sock"
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(system); err != nil {
			home, _ := os.UserHomeDir()
			for _, rel := range []string{".orbstack/run/docker.sock", ".docker/run/docker.sock", ".colima/default/docker.sock", ".rd/docker.sock"} {
				p := filepath.Join(home, rel)
				if _, err := os.Stat(p); err == nil {
					return Endpoint{Network: "unix", Address: p, Host: "unix://" + p}
				}
			}
		}
	}
	return Endpoint{Network: "unix", Address: system, Host: "unix://" + system}
}

func parseHost(h, ctx string) (Endpoint, error) {
	switch {
	case strings.HasPrefix(h, "unix://"):
		return Endpoint{Network: "unix", Address: strings.TrimPrefix(h, "unix://"), Host: h, Context: ctx}, nil
	case strings.HasPrefix(h, "tcp://"):
		return Endpoint{Network: "tcp", Address: strings.TrimPrefix(h, "tcp://"), Host: h, Context: ctx}, nil
	case h == "":
		return Endpoint{}, errors.New("docker context " + ctx + " has no engine endpoint")
	}
	return Endpoint{}, errors.New("unsupported docker endpoint " + h + " (only unix:// and tcp:// without TLS)")
}
