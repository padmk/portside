package docker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// dialer returns how to open one connection to the engine at ep. A setup
// problem, such as a missing ssh command or unreadable certificates, is
// returned by every dial.
func dialer(ep Endpoint, env []string) func(ctx context.Context) (net.Conn, error) {
	fail := func(err error) func(context.Context) (net.Conn, error) {
		return func(context.Context) (net.Conn, error) { return nil, err }
	}
	switch ep.Network {
	case "ssh":
		bin := lookPathIn("ssh", env)
		if bin == "" {
			return fail(errors.New("the ssh command was not found on PATH"))
		}
		return func(ctx context.Context) (net.Conn, error) { return dialSSH(ep, bin, env) }
	case "tcp":
		if ep.TLS {
			cfg, err := tlsConfig(ep)
			if err != nil {
				return fail(err)
			}
			d := &tls.Dialer{Config: cfg}
			return func(ctx context.Context) (net.Conn, error) { return d.DialContext(ctx, "tcp", ep.Address) }
		}
	}
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, ep.Network, ep.Address)
	}
}

// tlsConfig loads the certificates the docker CLI would use: ca.pem to
// check the engine, cert.pem and key.pem to identify this client.
func tlsConfig(ep Endpoint) (*tls.Config, error) {
	host, _, _ := net.SplitHostPort(ep.Address)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: !ep.TLSVerify}
	if ep.CertDir == "" {
		return cfg, nil
	}
	if ca, err := os.ReadFile(filepath.Join(ep.CertDir, "ca.pem")); err == nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, errors.New("no certificates in " + filepath.Join(ep.CertDir, "ca.pem"))
		}
		cfg.RootCAs = pool
	}
	cert, key := filepath.Join(ep.CertDir, "cert.pem"), filepath.Join(ep.CertDir, "key.pem")
	if _, err := os.Stat(cert); err == nil {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, errors.New("docker TLS client certificate: " + err.Error())
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

func lookPathIn(name string, env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if dirs, ok := strings.CutPrefix(env[i], "PATH="); ok {
			for _, dir := range filepath.SplitList(dirs) {
				p := filepath.Join(dir, name)
				if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
					return p
				}
			}
			break
		}
	}
	p, _ := exec.LookPath(name)
	return p
}

// SSHError is why ssh ended a connection, from its own output.
type SSHError struct{ Message string }

// Error names ssh once: its own messages often start with "ssh: ".
func (e *SSHError) Error() string {
	if strings.HasPrefix(e.Message, "ssh: ") {
		return e.Message
	}
	return "ssh: " + e.Message
}

// dialSSH runs `docker system dial-stdio` on the remote host, as the docker
// CLI does for ssh:// endpoints, and uses its standard input and output as
// the connection. Prompts are disabled: keys come from the agent or config.
// Keepalives end a connection whose link died (after sleep or a network
// change) within 45 seconds, so the event stream reconnects.
func dialSSH(ep Endpoint, bin string, env []string) (net.Conn, error) {
	args := []string{"-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "-o", "BatchMode=yes", "-T"}
	if ep.SSHUser != "" {
		args = append(args, "-l", ep.SSHUser)
	}
	if ep.SSHPort != "" {
		args = append(args, "-p", ep.SSHPort)
	}
	args = append(args, "--", ep.Address, "docker", "system", "dial-stdio")
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	c := &sshConn{in: inW, out: outR, exited: make(chan struct{})}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, &c.stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, err
	}
	c.cmd = cmd
	go func() {
		_ = cmd.Wait()
		c.mu.Lock()
		c.reaped = true
		c.mu.Unlock()
		close(c.exited)
	}()
	return c, nil
}

// sshConn is a connection over an ssh process's standard input and output.
// Pipes support deadlines, so it behaves like a socket.
type sshConn struct {
	cmd     *exec.Cmd
	in      *os.File
	out     *os.File
	stderr  lockedBuffer
	exited  chan struct{}
	gotData bool
	once    sync.Once

	mu     sync.Mutex
	reaped bool // ssh was waited for, so its pid and group may be reused
}

func (c *sshConn) Read(p []byte) (int, error) {
	n, err := c.out.Read(p)
	if n > 0 {
		c.gotData = true
	}
	if err == io.EOF && !c.gotData {
		select {
		case <-c.exited:
		case <-time.After(time.Second):
		}
		if msg := c.stderr.lastLine(); msg != "" {
			return n, &SSHError{Message: msg}
		}
	}
	return n, err
}

func (c *sshConn) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *sshConn) Close() error {
	c.once.Do(func() {
		c.in.Close()
		c.out.Close()
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.cmd.Process != nil && !c.reaped {
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	return nil
}

func (c *sshConn) LocalAddr() net.Addr  { return sshAddr{} }
func (c *sshConn) RemoteAddr() net.Addr { return sshAddr{} }

func (c *sshConn) SetDeadline(t time.Time) error {
	_ = c.in.SetWriteDeadline(t)
	return c.out.SetReadDeadline(t)
}

func (c *sshConn) SetReadDeadline(t time.Time) error  { return c.out.SetReadDeadline(t) }
func (c *sshConn) SetWriteDeadline(t time.Time) error { return c.in.SetWriteDeadline(t) }

type sshAddr struct{}

func (sshAddr) Network() string { return "ssh" }
func (sshAddr) String() string  { return "ssh" }

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() > 16<<10 {
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *lockedBuffer) lastLine() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(b.buf.String()), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
