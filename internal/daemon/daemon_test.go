package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
)

func serveTest(t *testing.T, c Client, services ...func(context.Context, Request) (any, error)) func(bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx, services...) }()
	wait(t, func() bool { _, err := c.Call(context.Background(), Request{Op: "status"}); return err == nil })
	var once sync.Once
	stop := func(preserve bool) {
		once.Do(func() {
			_, err := c.Call(context.Background(), Request{Op: "stop", Preserve: preserve})
			if err != nil {
				t.Error(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(10 * time.Second):
				cancel()
				t.Error("daemon shutdown timed out")
			}
			cancel()
		})
	}
	t.Cleanup(func() { stop(false) })
	return stop
}

func wait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func echo(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func roundtrip(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	_, err = c.Write([]byte("echo"))
	if err != nil {
		return false
	}
	b := make([]byte, 4)
	_, err = io.ReadFull(c, b)
	return err == nil && string(b) == "echo"
}

func TestSharedControlAndIndependentForwardRecovery(t *testing.T) {
	s := testssh.Start(t)
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	stop := serveTest(t, c)
	opts := sshx.Defaults()
	opts.ConfigFile = s.Config
	opts.ConnectTimeout = time.Second
	opts.ServerAliveInterval = time.Second
	opts.ServerAliveCountMax = 1
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	req := Request{Op: "acquire", Alias: s.Alias, SSH: opts}
	before := s.Accepted.Load()
	var wg sync.WaitGroup
	sockets := make(chan string, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := c.Call(ctx, req)
			if err != nil {
				t.Error(err)
				return
			}
			sockets <- state.Socket
		}()
	}
	wg.Wait()
	close(sockets)
	var socket string
	for got := range sockets {
		if socket != "" && got != socket {
			t.Fatal("concurrent acquisition created different masters")
		}
		socket = got
	}
	if n := s.Accepted.Load() - before; n != 1 {
		t.Fatalf("control created %d TCP connections, want 1", n)
	}
	for range 3 {
		out, err := sshx.Command(ctx, "ssh", (Transport{Socket: socket}).Args(false, "printf reused")...).Output()
		if err != nil || string(out) != "reused" {
			t.Fatalf("channel: %q %v", out, err)
		}
	}
	if n := s.Accepted.Load() - before; n != 1 {
		t.Fatalf("channel opened extra connection: %d", n)
	}
	port := freePort(t)
	f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", port, echo(t)))
	if err != nil {
		t.Fatal(err)
	}
	forwardReq := Request{Op: "forward", Alias: s.Alias, SSH: opts, Forwards: []config.Forward{f}, Policy: reconnect.Policy{Attempts: 30, Interval: 100 * time.Millisecond}}
	if _, err = c.Call(ctx, forwardReq); err != nil {
		t.Fatal(err)
	}
	if n := s.Accepted.Load() - before; n != 2 {
		t.Fatalf("forward must have an independent connection; total=%d", n)
	}
	if _, err = c.Call(ctx, forwardReq); err != nil {
		t.Fatal(err)
	}
	if n := s.Accepted.Load() - before; n != 2 {
		t.Fatalf("duplicate forward opened extra connection; total=%d", n)
	}
	if !roundtrip(port) {
		t.Fatal("forward failed")
	}
	s.Offline(true)
	time.Sleep(150 * time.Millisecond)
	s.Offline(false)
	defer s.Offline(false)
	wait(t, func() bool {
		state, err := c.Call(ctx, req)
		return err == nil && state.Socket != socket && roundtrip(port)
	})
	stop(true)
	serveTest(t, c)
	wait(t, func() bool { return roundtrip(port) })
	// Restart restores durable forwards; stop clears desired registrations.
	state, err := c.Call(ctx, Request{Op: "status"})
	if err != nil || len(state.Forwards) != 1 {
		t.Fatalf("restoration: %+v %v", state, err)
	}
}

func TestLeaseDisconnectKeepsOtherOwnersAndDurableForwards(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	serveTest(t, c)
	ctx := context.Background()
	port := freePort(t)
	f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", port, echo(t)))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Op: "forward", Local: true, Forwards: []config.Forward{f}, Policy: reconnect.Defaults().Forward}
	leaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, owner := range []string{"window1", "window2"} {
		if _, err = c.Call(ctx, Request{Op: "lease-start", Owner: owner}); err != nil {
			t.Fatal(err)
		}
		req.Owner = owner
		if _, err = c.Call(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	leaseDone := make(chan struct{})
	go func() { defer close(leaseDone); _, _ = c.Call(leaseCtx, Request{Op: "lease", Owner: "window1"}) }()
	// Closing a client's lifetime socket simulates abrupt CLI termination.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-leaseDone
	if !roundtrip(port) {
		t.Fatal("one exiting window removed the other window's forward")
	}
	if _, err = c.Call(ctx, Request{Op: "release", Owner: "window2"}); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
		}
		return err != nil
	})
	// An explicit service-only open makes the same listener durable.
	if _, err = c.Call(ctx, Request{Op: "lease-start", Owner: "window3"}); err != nil {
		t.Fatal(err)
	}
	req.Owner = "window3"
	if _, err = c.Call(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.Owner = ""
	if _, err = c.Call(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Call(ctx, Request{Op: "release", Owner: "window3"}); err != nil {
		t.Fatal(err)
	}
	if !roundtrip(port) {
		t.Fatal("shell release removed explicit background forward")
	}
	// Same listener, different target must fail and preserve the original.
	req.Forwards[0].TargetPort = freePort(t)
	if _, err = c.Call(ctx, req); err == nil {
		t.Fatal("conflicting listener was accepted")
	}
	if !roundtrip(port) {
		t.Fatal("conflict disrupted existing listener")
	}
}
