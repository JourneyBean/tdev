package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/testssh"
)

func TestDaemonOpenLifetimesAndCompletionReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated sshd acceptance runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.4.1-dev")
	root, remoteRoot := t.TempDir(), t.TempDir()
	daemonCleanup(t, root)
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
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()
	store := config.Store{Root: remoteRoot}
	p, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", port, listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(context.Background(), p.Name, p.Revision, func(p *config.Project) error { p.Forwards = []config.Forward{f}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	before := s.Accepted.Load()
	for range 3 {
		out, err := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "__complete", "open", "dev/de").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "dev/demo") {
			t.Fatalf("completion: %s %v", out, err)
		}
	}
	if n := s.Accepted.Load() - before; n != 0 {
		t.Fatalf("warm completion opened %d TCP connections", n)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "stop")
	var wg sync.WaitGroup
	pids := make(chan int, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := exec.Command(binary, "--config-dir", root, "--json", "daemon", "start").Output()
			if err != nil {
				t.Error(err)
				return
			}
			var r struct{ Data daemon.State }
			if err = json.Unmarshal(out, &r); err != nil {
				t.Error(err)
				return
			}
			pids <- r.Data.PID
		}()
	}
	wg.Wait()
	close(pids)
	pid := 0
	for got := range pids {
		if pid != 0 && got != pid {
			t.Fatal("concurrent daemon starts disagree")
		}
		pid = got
	}
	check := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
			if err == nil {
				c.Close()
			}
			if (err == nil) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("listener active=%v want %v", err == nil, want)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	first := terminalCLI(t, binary, root, s.Config, "open", "dev/demo")
	first.write(t, "printf 'FIRST_READY\\n'\r")
	first.waitText(t, "FIRST_READY\r\n")
	second := terminalCLI(t, binary, root, s.Config, "open", "dev/demo")
	second.write(t, "printf 'SECOND_READY\\n'\r")
	second.waitText(t, "SECOND_READY\r\n")
	check(true)
	if err = first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	first.exit(t, 130)
	check(true)
	second.write(t, "printf 'SECOND_STILL_READY\\n'\r")
	second.waitText(t, "SECOND_STILL_READY\r\n")
	// The remaining shell and its lease recover after a deliberate daemon restart.
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "restart")
	check(true)
	second.write(t, "printf 'AFTER_RESTART\\n'\r")
	second.waitText(t, "AFTER_RESTART\r\n")
	second.write(t, "tmux detach-client\r")
	second.exit(t, 0)
	check(false)
	// Service-only open returns without attaching and survives subsequent shell exit.
	executeBinary(t, binary, root, s.Config, "", 0, "open", "dev/demo", "--only", "forward")
	check(true)
	third := terminalCLI(t, binary, root, s.Config, "open", "dev/demo", "--only", "shell")
	third.write(t, "printf 'SHELL_ONLY_READY\\n'\r")
	third.waitText(t, "SHELL_ONLY_READY\r\n")
	third.write(t, "tmux detach-client\r")
	third.exit(t, 0)
	check(true)
	// Recover orphaned private masters and durable services after a hard crash.
	response := executeBinary(t, binary, root, s.Config, "", 0, "daemon", "status")
	data, _ := json.Marshal(response.Data)
	var crashed daemon.State
	if err = json.Unmarshal(data, &crashed); err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(crashed.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err = process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "start")
	check(true)
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "stop")
	check(false)
	// A deliberate stop remains stopped when a completion request arrives.
	out, err := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "__complete", "open", "dev/de").CombinedOutput()
	if err != nil || strings.Contains(string(out), "dev/demo\n") {
		t.Fatalf("stopped completion: %s %v", out, err)
	}
	client, err := daemon.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Call(context.Background(), daemon.Request{Op: "status"}); err == nil {
		t.Fatal("completion restarted a deliberately stopped daemon")
	}
	// Invalid feature selections fail before any services start.
	r := executeBinary(t, binary, root, s.Config, "", 2, "open", "dev/demo", "--only", "web")
	if r.Error == nil || r.Error.Code != "INVALID_CONFIG" {
		t.Fatal(r)
	}
}
