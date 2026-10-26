package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/tailnet"
)

// store keeps what outlives a daemon run in the state directory: the
// computer this one is connected to, so the connection resumes after a
// restart, and per computer the local ports chosen for remote ports and the
// remote ports not forwarded at all.
type store struct {
	path string
	mu   sync.Mutex
}

// saved is the connection to resume: the device as it was known when
// connecting (its tailnet addresses included) and the daemon's port there.
type saved struct {
	Name    string   `json:"name"`
	DNSName string   `json:"dnsName"`
	Label   string   `json:"label"`
	OS      string   `json:"os,omitempty"`
	IPs     []string `json:"ips"`
	Port    int      `json:"port"`
}

func savedFrom(n tailnet.Node, port int) *saved {
	return &saved{Name: n.Name, DNSName: n.DNSName, Label: n.Label, OS: n.OS, IPs: n.IPs, Port: port}
}

func (s saved) node() tailnet.Node {
	return tailnet.Node{Name: s.Name, DNSName: s.DNSName, Label: s.Label, OS: s.OS, IPs: s.IPs}
}

type storeData struct {
	Connection *saved `json:"connection,omitempty"`
	// Ports maps a computer's DNS name to its remote ports and the local
	// port each is forwarded to instead of the same number.
	Ports map[string]map[string]int `json:"ports,omitempty"`
	// Off maps a computer's DNS name to the remote ports whose forwarding
	// the user turned off.
	Off map[string][]int `json:"off,omitempty"`
}

func newStore(stateDir string) *store {
	return &store{path: filepath.Join(stateDir, "remote.json")}
}

func (s *store) load() storeData {
	var d storeData
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	return d
}

func (s *store) save(d storeData) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFile(s.path, b)
}

func (s *store) edit(fn func(*storeData)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.load()
	fn(&d)
	return s.save(d)
}

// connection returns the connection to resume, if any.
func (s *store) connection() (saved, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.load()
	if d.Connection == nil || d.Connection.DNSName == "" || d.Connection.Port <= 0 {
		return saved{}, false
	}
	return *d.Connection, true
}

// setConnection records the connection to resume; nil forgets it.
func (s *store) setConnection(c *saved) error {
	return s.edit(func(d *storeData) { d.Connection = c })
}

// movePort changes the saved connection's port when it is to dnsName.
func (s *store) movePort(dnsName string, port int) error {
	return s.edit(func(d *storeData) {
		if d.Connection != nil && strings.EqualFold(d.Connection.DNSName, dnsName) {
			d.Connection.Port = port
		}
	})
}

// forget removes the saved connection when it is to dnsName.
func (s *store) forget(dnsName string) error {
	return s.edit(func(d *storeData) {
		if d.Connection != nil && strings.EqualFold(d.Connection.DNSName, dnsName) {
			d.Connection = nil
		}
	})
}

// locals returns the remote-to-local port map of a computer.
func (s *store) locals(dnsName string) map[int]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]int{}
	for k, v := range s.load().Ports[strings.ToLower(dnsName)] {
		if p, err := strconv.Atoi(k); err == nil && v > 0 {
			out[p] = v
		}
	}
	return out
}

// setLocal forwards a computer's remote port to local; 0 or the same
// number removes the mapping.
func (s *store) setLocal(dnsName string, port, local int) error {
	return s.edit(func(d *storeData) {
		key := strings.ToLower(dnsName)
		m := d.Ports[key]
		if local == 0 || local == port {
			delete(m, strconv.Itoa(port))
		} else {
			if m == nil {
				m = map[string]int{}
			}
			m[strconv.Itoa(port)] = local
		}
		if d.Ports == nil {
			d.Ports = map[string]map[string]int{}
		}
		if len(m) == 0 {
			delete(d.Ports, key)
		} else {
			d.Ports[key] = m
		}
	})
}

// off returns the remote ports of a computer whose forwarding is off.
func (s *store) off(dnsName string) map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]bool{}
	for _, p := range s.load().Off[strings.ToLower(dnsName)] {
		if p > 0 {
			out[p] = true
		}
	}
	return out
}

// setOff turns forwarding of a computer's remote port off, or on again.
func (s *store) setOff(dnsName string, port int, off bool) error {
	return s.edit(func(d *storeData) {
		key := strings.ToLower(dnsName)
		next := []int{}
		for _, p := range d.Off[key] {
			if p != port {
				next = append(next, p)
			}
		}
		if off {
			next = append(next, port)
			slices.Sort(next)
		}
		if d.Off == nil {
			d.Off = map[string][]int{}
		}
		if len(next) == 0 {
			delete(d.Off, key)
		} else {
			d.Off[key] = next
		}
	})
}
