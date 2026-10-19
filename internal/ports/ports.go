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
	Address string `json:"address,omitempty"`
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// List returns all TCP listeners visible to the current user, one entry per
// (port, pid), sorted by port. It uses lsof and falls back to ss on Linux.
func List(ctx context.Context) []Listener {
	out, err := run(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
	if err != nil && len(out) == 0 {
		return listSS(ctx)
	}
	var res []Listener
	seen := map[[2]int]bool{}
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
			k := [2]int{port, pid}
			if seen[k] {
				continue
			}
			seen[k] = true
			res = append(res, Listener{Port: port, PID: pid, Command: cmd, Address: addr[:i]})
		}
	}
	fillCwd(ctx, res)
	sort.Slice(res, func(i, j int) bool {
		if res[i].Port != res[j].Port {
			return res[i].Port < res[j].Port
		}
		return res[i].PID < res[j].PID
	})
	return res
}

var ssRe = regexp.MustCompile(`:(\d+)\s.*users:\(\("([^"]+)",pid=(\d+)`)

func listSS(ctx context.Context) []Listener {
	out, err := run(ctx, "ss", "-ltnpH")
	if err != nil {
		return nil
	}
	var res []Listener
	seen := map[[2]int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		m := ssRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		port, _ := strconv.Atoi(m[1])
		pid, _ := strconv.Atoi(m[3])
		if k := [2]int{port, pid}; !seen[k] {
			seen[k] = true // IPv4 and IPv6 sockets of one process count once
			res = append(res, Listener{Port: port, PID: pid, Command: m[2]})
		}
	}
	fillCwd(ctx, res)
	sort.Slice(res, func(i, j int) bool { return res[i].Port < res[j].Port })
	return res
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
	for _, line := range strings.Split(string(out), "\n") {
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
		for _, line := range strings.Split(string(out), "\n") {
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
	for _, line := range strings.Split(string(out), "\n") {
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
	for _, line := range strings.Split(string(out), "\n") {
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
