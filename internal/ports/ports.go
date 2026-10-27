// Package ports answers "is anything listening on this port, and who is it".
package ports

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Listening reports whether a TCP connection to the port on loopback succeeds.
// Both IPv4 and IPv6 are tried because dev servers bind to either.
func Listening(port int) bool {
	if port <= 0 {
		return false
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 250*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// Listener is a process listening on a TCP port.
type Listener struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	// Addresses are the local addresses the process listens on at Port, as
	// lsof or ss write them ("127.0.0.1", "[::1]", "*"), in the order found.
	Addresses []string `json:"addresses,omitempty"`
}

// Same reports whether two listeners describe the same sockets.
func (l Listener) Same(o Listener) bool {
	return l.Port == o.Port && l.PID == o.PID && l.Command == o.Command && l.Cwd == o.Cwd && slices.Equal(l.Addresses, o.Addresses)
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// List returns all TCP listeners visible to the current user, one entry per
// (port, pid) with all of its addresses, sorted by port, with working
// directories. It uses lsof and falls back to ss on Linux.
func List(ctx context.Context) []Listener {
	res := Sockets(ctx)
	fillCwd(ctx, res)
	return res
}

// Sockets is List without working directories: one lsof (or ss) call.
func Sockets(ctx context.Context) []Listener {
	out, err := run(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
	if err != nil && len(out) == 0 {
		return listSS(ctx)
	}
	return parseLsof(out)
}

// listenerSet collects listeners, merging the sockets of one process on one
// port (IPv4 and IPv6, or several interfaces) into one entry.
type listenerSet struct {
	res []Listener
	at  map[[2]int]int
}

func (s *listenerSet) add(port, pid int, cmd, addr string) {
	if s.at == nil {
		s.at = map[[2]int]int{}
	}
	k := [2]int{port, pid}
	i, ok := s.at[k]
	if !ok {
		s.at[k] = len(s.res)
		s.res = append(s.res, Listener{Port: port, PID: pid, Command: cmd})
		i = len(s.res) - 1
	}
	if addr != "" && !slices.Contains(s.res[i].Addresses, addr) {
		s.res[i].Addresses = append(s.res[i].Addresses, addr)
	}
}

func (s *listenerSet) sorted() []Listener {
	sort.SliceStable(s.res, func(i, j int) bool {
		if s.res[i].Port != s.res[j].Port {
			return s.res[i].Port < s.res[j].Port
		}
		return s.res[i].PID < s.res[j].PID
	})
	return s.res
}

// parseLsof reads lsof -F pcn output.
func parseLsof(out []byte) []Listener {
	var set listenerSet
	var pid int
	var cmd string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			cmd = line[1:]
		case 'n':
			addr := line[1:]
			i := strings.LastIndexByte(addr, ':')
			if i < 0 {
				continue
			}
			port, err := strconv.Atoi(addr[i+1:])
			if err != nil {
				continue
			}
			set.add(port, pid, cmd, addr[:i])
		}
	}
	return set.sorted()
}

var ssRe = regexp.MustCompile(`:(\d+)\s.*users:\(\("([^"]+)",pid=(\d+)`)

func listSS(ctx context.Context) []Listener {
	out, err := run(ctx, "ss", "-ltnpH")
	if err != nil {
		return nil
	}
	return parseSS(string(out))
}

// parseSS reads ss -ltnpH output, whose fourth field is the local address
// ("0.0.0.0:22", "[::]:22", "*:22", "127.0.0.53%lo:53").
func parseSS(out string) []Listener {
	var set listenerSet
	for line := range strings.SplitSeq(out, "\n") {
		m := ssRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		port, _ := strconv.Atoi(m[1])
		pid, _ := strconv.Atoi(m[3])
		addr := ""
		if f := strings.Fields(line); len(f) >= 4 {
			if i := strings.LastIndexByte(f[3], ':'); i > 0 {
				addr = f[3][:i]
			}
		}
		set.add(port, pid, m[2], addr)
	}
	return set.sorted()
}

func fillCwd(ctx context.Context, ls []Listener) {
	if len(ls) == 0 {
		return
	}
	cwd := Cwds(ctx, pidsOf(ls))
	for i := range ls {
		ls[i].Cwd = cwd[ls[i].PID]
	}
}

func pidsOf(ls []Listener) []int {
	set := map[int]bool{}
	var out []int
	for _, l := range ls {
		if !set[l.PID] {
			set[l.PID] = true
			out = append(out, l.PID)
		}
	}
	return out
}

// Cwds returns the working directory of each pid, where it can be read.
func Cwds(ctx context.Context, pids []int) map[int]string {
	res := map[int]string{}
	if len(pids) == 0 {
		return res
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = strconv.Itoa(p)
	}
	// Linux exposes the working directory directly; elsewhere ask lsof.
	if _, err := os.Stat("/proc/self/cwd"); err == nil {
		for _, p := range pids {
			if dir, err := os.Readlink("/proc/" + strconv.Itoa(p) + "/cwd"); err == nil {
				res[p] = dir
			}
		}
		return res
	}
	out, _ := run(ctx, "lsof", "-a", "-d", "cwd", "-p", strings.Join(list, ","), "-F", "pn")
	var pid int
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			res[pid] = line[1:]
		}
	}
	return res
}

// IPv6Loopback reports whether the system has IPv6 loopback.
func IPv6Loopback() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// LoopbackListeners returns the pid listening on port at each loopback
// address ("127.0.0.1", "::1"), where lsof or ss can see it.
func LoopbackListeners(ctx context.Context, port int) map[string]int {
	res := map[string]int{}
	add := func(addr string, pid int) {
		host, p, err := net.SplitHostPort(addr)
		if err != nil || p != strconv.Itoa(port) || pid <= 0 {
			return
		}
		if host == "127.0.0.1" || host == "::1" {
			if _, seen := res[host]; !seen {
				res[host] = pid
			}
		}
	}
	out, err := run(ctx, "lsof", "-nP", "-w", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-F", "pn")
	if err != nil && len(out) == 0 {
		out, _ = run(ctx, "ss", "-ltnpH", "sport", "=", ":"+strconv.Itoa(port))
		for line := range strings.SplitSeq(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 4 {
				continue
			}
			if m := ssPidRe.FindStringSubmatch(line); m != nil {
				pid, _ := strconv.Atoi(m[1])
				add(f[3], pid)
			}
		}
		return res
	}
	pid := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			add(line[1:], pid)
		}
	}
	return res
}

var ssPidRe = regexp.MustCompile(`pid=(\d+)`)

// Owner returns the first listener on port, if lsof or ss can see it.
func Owner(ctx context.Context, port int) (Listener, bool) {
	out, err := run(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-F", "pc")
	if err != nil && len(out) == 0 {
		for _, l := range listSS(ctx) {
			if l.Port == port {
				return l, true
			}
		}
		return Listener{}, false
	}
	l := Listener{Port: port}
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			if l.PID != 0 {
				break
			}
			l.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			if l.Command == "" {
				l.Command = line[1:]
			}
		}
	}
	if l.PID == 0 {
		return Listener{}, false
	}
	if c := Cwds(ctx, []int{l.PID}); c[l.PID] != "" {
		l.Cwd = c[l.PID]
	}
	return l, true
}
