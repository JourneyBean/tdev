package forward

import (
	"context"
	"fmt"
	"io"
	"net"
	"tdev/internal/config"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func waitState(t *testing.T, g *Group, state string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-g.Events:
			if !ok {
				t.Fatalf("closed waiting for %s: %v", state, g.Failure())
			}
			if e.State == state {
				return
			}
			if e.State == "failed" {
				t.Fatal(e)
			}
		case <-timer.C:
			t.Fatal("timeout waiting for", state)
		}
	}
}
func roundtrip(t *testing.T, port int) {
	t.Helper()
	c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmtInt(port)), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, e = c.Write([]byte("tdev-echo"))
	if e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 9)
	if _, e = io.ReadFull(c, b); e != nil || string(b) != "tdev-echo" {
		t.Fatalf("roundtrip %q %v", b, e)
	}
}
func fmtInt(n int) string { return fmt.Sprintf("%d", n) }
func TestRealForwardReadinessRecoveryAndConflict(t *testing.T) {
	s := testssh.Start(t)
	opts := sshx.Defaults()
	opts.ConfigFile = s.Config
	opts.ConnectTimeout = 2 * time.Second
	opts.ServerAliveInterval = time.Second
	opts.ServerAliveCountMax = 1
	echo, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	targetPort := echo.Addr().(*net.TCPAddr).Port
	for _, direction := range []string{"L", "R"} {
		t.Run(direction, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer s.Offline(false)
			p := freePort(t)
			f, e := config.ParseForward(direction, fmtInt(p)+":"+fmtInt(targetPort))
			if e != nil {
				t.Fatal(e)
			}
			g := Start(ctx, s.Alias, opts, []config.Forward{f}, reconnect.Policy{Attempts: 20, Interval: 50 * time.Millisecond})
			defer g.Close()
			waitState(t, g, "ready")
			roundtrip(t, p)
			// A competing client must fail without taking over or stopping the existing listener.
			other := Start(ctx, s.Alias, opts, []config.Forward{f}, reconnect.Policy{Attempts: 0, Interval: time.Millisecond})
			for e := range other.Events {
				if e.State == "ready" {
					t.Fatal("competing listener succeeded")
				}
			}
			if other.Failure() == nil {
				t.Fatal("missing conflict")
			}
			other.Close()
			roundtrip(t, p)
			s.Offline(true)
			waitState(t, g, "waiting")
			s.Offline(false)
			waitState(t, g, "ready")
			roundtrip(t, p)
			g.Close()
			if c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmtInt(p)), 100*time.Millisecond); e == nil {
				c.Close()
				t.Fatal("owned listener remains after cancellation")
			}
		})
	}
}

func TestLocalForwardsDataConflictAndCancellation(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	f := config.Forward{ID: "local", Direction: "L", BindAddress: "127.0.0.1", ListenPort: freePort(t), TargetHost: "127.0.0.1", TargetPort: echo.Addr().(*net.TCPAddr).Port}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	group := StartLocal(ctx, []config.Forward{f})
	defer group.Close()
	waitState(t, group, "ready")
	roundtrip(t, f.ListenPort)
	conflict := StartLocal(ctx, []config.Forward{f})
	defer conflict.Close()
	for event := range conflict.Events {
		if event.State == "failed" {
			break
		}
	}
	if conflict.Failure() == nil {
		t.Fatal("conflicting listener was accepted")
	}
	roundtrip(t, f.ListenPort)
	cancel()
	group.Close()
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmtInt(f.ListenPort)), time.Second); err == nil {
		conn.Close()
		t.Fatal("local listener survived cancellation")
	}
}
