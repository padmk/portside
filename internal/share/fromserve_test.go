package share

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/padmk/portside/internal/ports"
)

func TestServeListenerAcceptsOnlyRootAndThisAccount(t *testing.T) {
	cases := []struct {
		name   string
		owner  ports.ConnOwner
		err    error
		accept bool
	}{
		{"root (tailscaled)", ports.ConnOwner{UID: 0}, nil, true},
		{"this account", ports.ConnOwner{UID: 501}, nil, true},
		{"another local account", ports.ConnOwner{UID: 1002}, nil, false},
		{"owner not found", ports.ConnOwner{}, ports.ErrNoOwner, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ln := serveListener{raw, 501, func(context.Context, netip.AddrPort, netip.AddrPort) (ports.ConnOwner, error) {
				return c.owner, c.err
			}}
			defer ln.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				if conn, err := ln.Accept(); err == nil {
					accepted <- conn
				}
			}()
			conn, err := net.Dial("tcp", raw.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			select {
			case got := <-accepted:
				got.Close()
				if !c.accept {
					t.Error("the connection was accepted")
				}
			case <-time.After(300 * time.Millisecond):
				if c.accept {
					t.Fatal("the connection was not accepted")
				}
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
					t.Errorf("the refused connection stayed open: %v", err)
				}
			}
		})
	}
}
