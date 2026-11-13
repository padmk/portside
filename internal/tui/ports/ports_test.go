package ports

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
)

func listener(port, pid int, cmd string) Listener {
	return Listener{Listener: ports.Listener{Port: port, PID: pid, Command: cmd, Addresses: []string{"127.0.0.1"}}}
}

func TestRolesAndOffReasons(t *testing.T) {
	env := &ui.Env{State: &server.State{Servers: []supervisor.View{
		{Name: "web", Status: supervisor.Running, PID: 10},
		{Name: "api", Status: supervisor.Stopped, Port: 3002},
	}}}
	managed := listener(3001, 10, "node")
	managed.Server, managed.Managed = "web", true
	self := listener(7878, 1, "portside")
	self.Server, self.Managed, self.Protected = "portside", true, "this is Portside itself"
	blocks := listener(3002, 20, "python3")
	blocks.Server = "api"
	other := listener(5000, 30, "ControlCenter")
	other.Addresses = []string{"*"}
	for _, c := range []struct {
		l          Listener
		describe   string
		kill, take string
	}{
		{managed, "server web", "", "no configured server waits for this port"},
		{self, "portside", "protected: this is Portside itself", "no configured server waits for this port"},
		{blocks, "blocks api", "", "its port is not held by another process"},
		{other, "unmanaged", "", "no configured server waits for this port"},
	} {
		if got := Describe(c.l); got != c.describe {
			t.Errorf("port %d describes as %q, want %q", c.l.Port, got, c.describe)
		}
		if got := Off(env, "kill", c.l); got != c.kill {
			t.Errorf("kill on %d: %q, want %q", c.l.Port, got, c.kill)
		}
		if got := Off(env, "takeover", c.l); got != c.take {
			t.Errorf("takeover on %d: %q, want %q", c.l.Port, got, c.take)
		}
	}
	if !KillStops(managed) || KillStops(other) {
		t.Error("kill stops a managed server and kills anything else")
	}
	if Reach(other) != "all interfaces" || Reach(managed) != "local" {
		t.Errorf("reach %q, %q", Reach(other), Reach(managed))
	}
}

func TestKillDialogNamesProcessPIDAndPort(t *testing.T) {
	msg := ConfirmKill(listener(3002, 4242, "python3"), nil)()
	c, ok := msg.(ui.ConfirmMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	text := c.Title + " " + c.Body
	for _, want := range []string{"port 3002", "python3", "pid 4242"} {
		if !strings.Contains(text, want) {
			t.Errorf("dialog %q lacks %q", text, want)
		}
	}
	if c.Confirm != "Kill" {
		t.Errorf("confirm label %q", c.Confirm)
	}
}

func TestRowsSortByPortAndFilter(t *testing.T) {
	p := New()
	p.list = []Listener{listener(5000, 3, "c"), listener(80, 1, "a"), listener(3000, 2, "b")}
	var got []int
	for _, l := range p.rows() {
		got = append(got, l.Port)
	}
	if len(got) != 3 || got[0] != 80 || got[1] != 3000 || got[2] != 5000 {
		t.Errorf("rows %v", got)
	}
	p.query = ":30"
	if rows := p.rows(); len(rows) != 1 || rows[0].Port != 3000 {
		t.Errorf("filter :30 keeps %v", rows)
	}
}

func TestKillArgumentNamesAPortOrAPID(t *testing.T) {
	p := New()
	p.open, p.loaded = true, true
	twin := listener(4000, 8, "nginx")
	p.list = []Listener{listener(3016, 5141, "node"), listener(3017, 5141, "node"), listener(4000, 7, "nginx"), twin}
	env := &ui.Env{}
	for _, c := range []struct {
		arg  string
		want ui.Selection
		err  string
	}{
		{"3016", ui.PortItem(5141, 3016), ""},
		{":3017", ui.PortItem(5141, 3017), ""},
		{"5141", ui.PortItem(5141, 3016), ""},
		{"4000", ui.Selection{}, "several processes listen on port 4000 (nginx 7, nginx 8)"},
		{"8", ui.PortItem(8, 4000), ""},
		{"9999", ui.Selection{}, "nothing listens on port 9999"},
		{"web", ui.Selection{}, `"web" is not a port or a PID`},
	} {
		msg := p.Kill(env, c.arg)()
		switch m := msg.(type) {
		case ui.ActMsg:
			if m.ID != "kill" || m.Item != c.want || c.err != "" {
				t.Errorf(":kill %s runs %+v, want %+v %q", c.arg, m, c.want, c.err)
			}
		case ui.NoticeMsg:
			if c.err == "" || !strings.Contains(m.Text, c.err) {
				t.Errorf(":kill %s says %q, want %q", c.arg, m.Text, c.err)
			}
		default:
			t.Errorf(":kill %s: %T", c.arg, msg)
		}
	}
}

func TestKillWaitsForTheFirstListAndRefreshKeepsTheCursor(t *testing.T) {
	p := New()
	env := &ui.Env{}
	p.Kill(env, "3016")
	if !p.open || p.pending != "3016" {
		t.Fatalf("a closed pane opens for :kill: open %v, pending %q", p.open, p.pending)
	}
	cmd := p.Update(env, listMsg{gen: p.gen, list: []Listener{listener(3000, 1, "a"), listener(3016, 5141, "node")}})
	// The kill comes first; the other command is the next listing's tick.
	c, _ := cmd().(tea.BatchMsg)[0]().(ui.ActMsg)
	found := c.Item == ui.PortItem(5141, 3016)
	if !found || p.pending != "" {
		t.Fatalf("the list did not run the pending kill (pending %q)", p.pending)
	}
	// A refresh keeps the cursor on its listener; while the target is
	// another pane's, it stays so.
	env.Target = ui.ServerItem("web")
	p.cur = ui.PortItem(5141, 3016)
	p.Update(env, listMsg{gen: p.gen, list: []Listener{listener(2000, 9, "z"), listener(3000, 1, "a"), listener(3016, 5141, "node")}})
	if l, _ := p.Cursor(); l.Port != 3016 || env.Target != ui.ServerItem("web") {
		t.Errorf("refresh moved the cursor to %d, target %+v", l.Port, env.Target)
	}
	// Closed, the pane drops its pending ticks.
	p.Close(env)
	if p.Update(env, tickMsg{gen: p.gen}) != nil {
		t.Error("a closed pane keeps listing")
	}
}

// TestRemoteAuxiliaryPortHasNoProxyFallback: on another computer a
// server's own port opens through the proxy, but another of its ports
// without a forwarded port has no address, and says so.
func TestRemoteAuxiliaryPortHasNoProxyFallback(t *testing.T) {
	cn := &client.Connection{LocalPort: 7878}
	cn.Peer.Name, cn.Peer.Label = "mbp", "mbp"
	env := &ui.Env{Session: ui.Session{Computer: "mbp", Conn: cn},
		State: &server.State{Servers: []supervisor.View{{Name: "web", Status: supervisor.Running, PID: 10, Port: 3000}}}}
	own := listener(3000, 10, "node")
	own.Server, own.Managed = "web", true
	aux := listener(5432, 10, "node")
	aux.Server, aux.Managed = "web", true
	if got := URL(env, own); got != "http://web.mbp.localhost:7878" {
		t.Errorf("the server's port opens at %q", got)
	}
	if got := URL(env, aux); got != "" {
		t.Errorf("an auxiliary port without forwarding opens at %q", got)
	}
	if got := Off(env, "open", aux); got != "no local address for :5432 on mbp" {
		t.Errorf("open on 5432: %q", got)
	}
	cn.Mirrors = append(cn.Mirrors, struct {
		Port  int    `json:"port"`
		Local int    `json:"local"`
		State string `json:"state"`
	}{5432, 15432, "mirrored"})
	if got := URL(env, aux); got != "http://localhost:15432" {
		t.Errorf("a forwarded auxiliary port opens at %q", got)
	}
}

// A takeover goes ahead while the confirmed process still listens on the
// port, also among the workers of a prefork server sharing its socket,
// and only on a holder the daemon could name.
func TestTakeoverChecksTheConfirmedHolder(t *testing.T) {
	holder := listener(3002, 20, "gunicorn")
	workers := []Listener{listener(3002, 20, "gunicorn"), listener(3002, 21, "gunicorn"), listener(4000, 9, "x")}
	if got := changedHolder(workers, holder); got != nil {
		t.Errorf("the confirmed master among its workers: changed to %+v", got)
	}
	if got := changedHolder([]Listener{listener(3002, 21, "gunicorn"), listener(3002, 20, "gunicorn")}, holder); got != nil {
		t.Errorf("the confirmed process listed after a worker: changed to %+v", got)
	}
	if got := changedHolder([]Listener{listener(3002, 55, "node")}, holder); got == nil || got.PID != 55 {
		t.Errorf("another process on the port: %+v", got)
	}
	if got := changedHolder(nil, holder); got != nil {
		t.Errorf("a free port: %+v", got)
	}
	env := &ui.Env{State: &server.State{Servers: []supervisor.View{
		{Name: "api", Status: supervisor.Blocked, Port: 3002, External: &ports.Listener{Port: 3002}},
	}}}
	if why := TakeoverOff(env, "api"); !strings.Contains(why, "not known") {
		t.Errorf("takeover with an unknown holder: %q", why)
	}
}
