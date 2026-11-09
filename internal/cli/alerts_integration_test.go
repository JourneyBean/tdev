package cli

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestAlertTitlesThroughAttach(t *testing.T) {
	binary := buildCLI(t, "0.4.1")
	for _, remote := range []bool{false, true} {
		name := "local"
		if remote {
			name = "ssh"
		}
		t.Run(name, func(t *testing.T) {
			if remote && runtime.GOOS != "linux" {
				t.Skip("private sshd integration runs on Linux")
			}
			root, sessionRoot := t.TempDir(), t.TempDir()
			socket := "tdev-alert-test-" + config.NewID()
			sshConfig, target := "", "local/demo"
			if remote {
				s := testssh.Start(t)
				socket, sshConfig, target = s.Socket, s.Config, "fixture/demo"
				executeBinary(t, binary, root, sshConfig, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", sessionRoot)
			} else {
				sessionRoot = root
			}
			t.Setenv("TMUX", "")
			t.Setenv("TDEV_TMUX_SOCKET", socket)
			m := tmux.Manager{Store: config.Store{Root: sessionRoot}, Socket: socket}
			run := func(args ...string) string {
				t.Helper()
				out, err := exec.Command("tmux", m.Args(args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("tmux %s: %v %s", args[0], err, out)
				}
				return strings.TrimSpace(string(out))
			}
			t.Cleanup(func() { _ = exec.Command("tmux", m.Args("kill-server")...).Run() })
			p, err := m.Store.Create(context.Background(), "demo", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			// A pre-existing managed session has no title configuration. Opening
			// it must install hooks before the first local or SSH attachment.
			session := run("new-session", "-d", "-P", "-F", "#{session_id}", "-s", tmux.SessionName(p), "-n", "main", "-c", p.Directory)
			for _, opt := range [][2]string{{"@tdev_project_id", p.ID}, {"@tdev_initialization", "ready"}, {"@tdev_generation", config.NewID()}} {
				run("set-option", "-t", session, opt[0], opt[1])
			}
			other := run("new-window", "-d", "-P", "-F", "#{window_id}", "-t", session+":", "-n", "other")
			attached := terminalCLI(t, binary, root, sshConfig, "open", target)
			wait := func(check func() bool) {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for !check() {
					if time.Now().After(deadline) {
						t.Fatal("attach alert state did not converge")
					}
					time.Sleep(15 * time.Millisecond)
				}
			}
			title := func(want string) {
				t.Helper()
				wait(func() bool {
					parts := strings.Split(attached.output.String(), "\x1b]0;")
					return len(parts) > 1 && strings.HasPrefix(parts[len(parts)-1], want+"\a")
				})
			}
			title("tdev-project-demo:main")
			attached.write(t, "\x1b[O")
			wait(func() bool { return run("show-options", "-qv", "-t", session, "@tdev_terminal_focused") == "0" })
			run("send-keys", "-t", session+":main", "printf '\\007'", "Enter")
			title("[!] tdev-project-demo:main")
			// Reattachment alone is not an acknowledgement of the pending bell.
			run("detach-client", "-s", session)
			attached.exit(t, 0)
			attached = terminalCLI(t, binary, root, sshConfig, "open", target)
			title("[!] tdev-project-demo:main")
			run("send-keys", "-t", other, "printf '\\007'", "Enter")
			wait(func() bool { return run("display-message", "-p", "-t", other, "#{window_bell_flag}") == "1" })
			attached.write(t, "\x1b[I")
			wait(func() bool { return run("show-options", "-wqv", "-t", session+":main", "@tdev_unfocused_bell") == "0" })
			title("[!] tdev-project-demo:main")
			run("select-window", "-t", other)
			title("tdev-project-demo:other")
			run("detach-client", "-s", session)
			attached.exit(t, 0)
		})
	}
}
