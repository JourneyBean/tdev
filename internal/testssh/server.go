// Package testssh runs a private, unprivileged sshd and fault-injection TCP proxy.
// Test identities and host keys live only in testing temporary directories.
package testssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type Server struct {
	Dir, Config, Alias string
	Port               int
	Socket             string
	Accepted           atomic.Int64
	listener           net.Listener
	mu                 sync.Mutex
	offline            bool
	conns              map[net.Conn]bool
	ctx                context.Context
	cancel             context.CancelFunc
}

func Start(t *testing.T) *Server {
	t.Helper()
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		for _, p := range []string{"/usr/sbin/sshd", "/usr/local/sbin/sshd"} {
			if _, e := os.Stat(p); e == nil {
				sshd = p
				err = nil
				break
			}
		}
	}
	if err != nil {
		if os.Getenv("TDEV_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("sshd integration dependency missing")
		}
		t.Skip("sshd not installed")
	}
	dir := t.TempDir()
	for _, key := range []string{"host", "client"} {
		c := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, key))
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("test key generation: %v %s", e, b)
		}
	}
	listen, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listen.Addr().(*net.TCPAddr).Port
	listen.Close()
	u, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	text := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s/host\nPidFile %s/pid\nAuthorizedKeysFile %s/client.pub\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nAllowUsers %s\nAllowTcpForwarding yes\nGatewayPorts no\nSubsystem sftp /usr/lib/openssh/sftp-server\nLogLevel VERBOSE\nSetEnv TDEV_TMUX_SOCKET=tdev-sshd-%d\n", port, dir, dir, dir, u.Username, port)
	sshdConfig := filepath.Join(dir, "sshd_config")
	if e = os.WriteFile(sshdConfig, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	log, e := os.Create(filepath.Join(dir, "sshd.log"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, sshd, "-D", "-e", "-f", sshdConfig)
	cmd.Stderr = log
	if e = cmd.Start(); e != nil {
		cancel()
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		_ = log.Close()
		_ = exec.Command("tmux", "-L", fmt.Sprintf("tdev-sshd-%d", port), "kill-server").Run()
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 30*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(log.Name())
			t.Fatalf("test sshd did not start: %s", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	proxy, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	proxyPort := proxy.Addr().(*net.TCPAddr).Port
	s := &Server{Dir: dir, Alias: "fixture", Port: proxyPort, Socket: fmt.Sprintf("tdev-sshd-%d", port), listener: proxy, conns: map[net.Conn]bool{}, ctx: ctx, cancel: cancel}
	pub, _ := os.ReadFile(filepath.Join(dir, "host.pub"))
	fields := strings.Fields(string(pub))
	known := filepath.Join(dir, "known_hosts")
	if e = os.WriteFile(known, []byte(fmt.Sprintf("[127.0.0.1]:%d %s %s\n", proxyPort, fields[0], fields[1])), 0600); e != nil {
		t.Fatal(e)
	}
	s.Config = filepath.Join(dir, "ssh_config")
	cfg := fmt.Sprintf("Host fixture\n HostName 127.0.0.1\n Port %d\n User %s\n IdentityFile %s/client\n IdentitiesOnly yes\n UserKnownHostsFile %s\n StrictHostKeyChecking yes\n LogLevel ERROR\n", proxyPort, u.Username, dir, known)
	if e = os.WriteFile(s.Config, []byte(cfg), 0600); e != nil {
		t.Fatal(e)
	}
	go s.proxy(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	t.Cleanup(func() { _ = proxy.Close(); s.Offline(true) })
	return s
}
func (s *Server) Offline(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline = v
	if v {
		for c := range s.conns {
			_ = c.Close()
		}
	}
}
func (s *Server) proxy(destination string) {
	for {
		client, e := s.listener.Accept()
		if e != nil {
			return
		}
		s.Accepted.Add(1)
		s.mu.Lock()
		if s.offline {
			client.Close()
			s.mu.Unlock()
			continue
		}
		s.conns[client] = true
		s.mu.Unlock()
		go func() {
			defer func() { client.Close(); s.mu.Lock(); delete(s.conns, client); s.mu.Unlock() }()
			remote, e := net.Dial("tcp", destination)
			if e != nil {
				return
			}
			defer remote.Close()
			done := make(chan struct{}, 1)
			go func() { _, _ = io.Copy(remote, client); done <- struct{}{} }()
			_, _ = io.Copy(client, remote)
			_ = client.Close()
			_ = remote.Close()
			<-done
		}()
	}
}
