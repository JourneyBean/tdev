package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/daemon"
	"tdev/internal/testssh"
)

func TestTemporaryServerForwards(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated SSH integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.5.0-dev")
	root := t.TempDir()
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	target := listener.Addr().(*net.TCPAddr).Port
	freePort := func() int {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	echo := func(port int) bool {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			return false
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(time.Second))
		if _, err = c.Write([]byte("hello")); err != nil {
			return false
		}
		b := make([]byte, 5)
		_, err = io.ReadFull(c, b)
		return err == nil && string(b) == "hello"
	}
	wait := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !fn() {
			if time.Now().After(deadline) {
				t.Fatal("condition timed out")
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	client, _ := daemon.New(root)
	for _, server := range []string{"local", "dev"} {
		t.Run(server, func(t *testing.T) {
			lp, rp := freePort(), freePort()
			response := executeBinary(t, binary, root, s.Config, "", 0, "forward", server, "-L", fmt.Sprintf("%d:localhost:%d", lp, target), "-R", fmt.Sprintf("%d:localhost:%d", rp, target))
			b, _ := json.Marshal(response.Data)
			var f temporaryForward
			if err := json.Unmarshal(b, &f); err != nil || f.Server != server || !strings.HasPrefix(f.Name, "forward-") || len(f.Forwards) != 2 {
				t.Fatalf("forward response: %s %v", b, err)
			}
			if !echo(lp) || !echo(rp) {
				t.Fatal("background forward did not carry traffic")
			}
			listing := executeBinary(t, binary, root, s.Config, "", 0, "ls", "--type", "forward", "--server", server)
			b, _ = json.Marshal(listing.Data)
			if !bytes.Contains(b, []byte(f.Name)) || !bytes.Contains(b, []byte(`"ready"`)) {
				t.Fatalf("forward missing from listing: %s", b)
			}
			fullListing := executeBinary(t, binary, root, s.Config, "", 0, "ls", "--server", server)
			b, _ = json.Marshal(fullListing.Data)
			if !bytes.Contains(b, []byte(f.Name)) {
				t.Fatalf("forward missing from ordinary ls: %s", b)
			}
			// A failed batch must remove its successful listener and recovery registration.
			other := freePort()
			executeBinary(t, binary, root, s.Config, "", 6, "forward", server, "-L", fmt.Sprintf("%d:%d", other, target), "-L", fmt.Sprintf("%d:%d", lp, target))
			wait(func() bool {
				l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", other))
				if err != nil {
					return false
				}
				l.Close()
				return true
			})
			if !echo(lp) {
				t.Fatal("conflict disrupted an existing forward")
			}
			executeBinary(t, binary, root, s.Config, "", 0, "daemon", "restart")
			wait(func() bool { return echo(lp) && echo(rp) })
			executeBinary(t, binary, root, s.Config, "", 0, "stop", server+"/"+f.Name, "-y")
			if echo(lp) || echo(rp) {
				t.Fatal("stop retained listeners")
			}
			state, err := client.Call(context.Background(), daemon.Request{Op: "status"})
			if err != nil || len(state.Forwards) != 0 {
				t.Fatalf("stopped group retained: %+v %v", state, err)
			}
			executeBinary(t, binary, root, s.Config, "", 0, "daemon", "restart")
			state, err = client.Call(context.Background(), daemon.Request{Op: "status"})
			if err != nil || len(state.Forwards) != 0 {
				t.Fatalf("stopped group restored: %+v %v", state, err)
			}
		})
	}
	for _, mode := range []struct {
		server string
		signal os.Signal
		exit   int
	}{
		{"local", os.Interrupt, 130}, {"dev", os.Interrupt, 130}, {"dev", os.Kill, -1},
	} {
		t.Run(fmt.Sprintf("foreground-%s-%v", mode.server, mode.signal), func(t *testing.T) {
			port := freePort()
			// A foreground command outlives the ordinary noninteractive operation timeout.
			cmd := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "--json", "--timeout", "1s", "forward", mode.server, "--foreground", "-L", fmt.Sprintf("%d:%d", port, target))
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			wait(func() bool { return echo(port) })
			timer := time.NewTimer(1200 * time.Millisecond)
			defer timer.Stop()
			select {
			case err := <-done:
				t.Fatalf("foreground exited early: %v %s", err, output.String())
			case <-timer.C:
			}
			listing := executeBinary(t, binary, root, s.Config, "", 0, "ls", "--type", "forward", "--server", mode.server)
			b, _ := json.Marshal(listing.Data)
			if !bytes.Contains(b, []byte("forward-")) {
				t.Fatalf("foreground absent from ls: %s", b)
			}
			if err := cmd.Process.Signal(mode.signal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != mode.exit {
					t.Fatalf("interrupt: %v %s", err, output.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatal("foreground did not exit")
			}
			wait(func() bool {
				state, err := client.Call(context.Background(), daemon.Request{Op: "status"})
				return err == nil && len(state.Forwards) == 0 && !echo(port)
			})
			if echo(port) {
				t.Fatal("foreground leaked listener")
			}
		})
	}
	if _, err := os.Stat(root + "/projects"); !os.IsNotExist(err) {
		t.Fatalf("temporary forwards created project configuration: %v", err)
	}
}
