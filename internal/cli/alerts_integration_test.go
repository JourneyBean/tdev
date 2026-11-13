package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

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
				executeBinary(t, binary, root, sshConfig, "", 0, "setup", "--name", "other-connection", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", sessionRoot)
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
			for _, opt := range [][2]string{{"@tdev_project_id", p.ID}, {"@tdev_project_name", p.Name}, {"@tdev_initialization", "ready"}, {"@tdev_generation", config.NewID()}, {"@tdev_runtime_config_version", "1"}, {"set-titles-string", "[!] legacy-title"}} {
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
					parts := strings.Split(attached.output.String(), "\x1b]2;")
					return len(parts) > 1 && strings.HasPrefix(parts[len(parts)-1], want+"\a")
				})
			}
			baseTitle := "tdev: " + target
			title(baseTitle)
			wait(func() bool {
				return run("show-options", "-qv", "-t", session, "@tdev_runtime_config_version") == fmt.Sprint(tmux.RuntimeConfigVersion)
			})
			stateFormat := run("show-options", "-v", "-t", session, "set-titles-string")
			if strings.Contains(stateFormat, "fixture") || strings.Contains(stateFormat, "local/") {
				t.Fatal("client connection name stored in server title format")
			}
			if err := pty.Setsize(attached.terminal, &pty.Winsize{Rows: 40, Cols: 110}); err != nil {
				t.Fatal(err)
			}
			wait(func() bool {
				return run("list-clients", "-t", session, "-F", "#{client_width}:#{client_height}") == "110:40"
			})
			// Raw input must deliver Ctrl-C to the pane, leaving the client alive.
			attached.write(t, "sleep 30\r")
			wait(func() bool {
				return run("display-message", "-p", "-t", session+":main", "#{pane_current_command}") == "sleep"
			})
			attached.write(t, "\x03printf 'CONTROL_READY\\n'\r")
			attached.waitText(t, "CONTROL_READY\r\n")
			// Ordinary application OSC remains available as tmux's pane title.
			run("send-keys", "-t", session+":main", "printf '\\033]2;pane-owned\\007'", "Enter")
			wait(func() bool {
				return run("display-message", "-p", "-t", session+":main", "#{pane_title}") == "pane-owned"
			})
			title(baseTitle)
			// A peer changing its title template cannot replace the client title.
			run("set-option", "-t", session, "set-titles-string", "PEER_TITLE")
			run("refresh-client", "-S", "-t", run("list-clients", "-t", session, "-F", "#{client_name}"))
			run("set-option", "-t", session, "set-titles-string", stateFormat)
			// tmux unwraps the first DCS layer. Exercise both direct OSC and a
			// remaining nested passthrough envelope at the client filter.
			run("set-option", "-w", "-t", session+":main", "allow-passthrough", "on")
			wrap := func(s string) string { return "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\" }
			otherOSC := "\x1b]52;c;ZGVtbw==\a\x1b]8;;https://example.invalid\x1b\\link\x1b]8;;\x1b\\\a"
			for _, nested := range []bool{false, true} {
				payload := "\x1b]0;INJECTED_ZERO\a\x1b]1;INJECTED_ICON\x1b\\\x1b]2;INJECTED_TWO\a" + otherOSC
				want := otherOSC
				if nested {
					payload, want = wrap(payload), wrap(want)
				}
				var command strings.Builder
				command.WriteString("printf '")
				for _, b := range []byte(wrap(payload)) {
					fmt.Fprintf(&command, "\\%03o", b)
				}
				command.WriteString("'")
				run("send-keys", "-t", session+":main", command.String(), "Enter")
				attached.waitText(t, want)
				title(baseTitle)
			}
			for _, code := range []string{"0", "1", "2"} {
				if strings.Contains(attached.output.String(), "\x1b]"+code+";INJECTED") || strings.Contains(attached.output.String(), "\x1b]"+code+";PEER_TITLE") {
					t.Fatal("peer title OSC reached outer terminal")
				}
			}
			attached.write(t, "\x1b[O")
			wait(func() bool { return run("show-options", "-qv", "-t", session, "@tdev_terminal_focused") == "0" })
			run("send-keys", "-t", session+":main", "printf '\\007'", "Enter")
			title("[!] " + baseTitle)
			// Reattachment alone is not an acknowledgement of the pending bell.
			run("detach-client", "-s", session)
			attached.exit(t, 0)
			attached = terminalCLI(t, binary, root, sshConfig, "open", target)
			title("[!] " + baseTitle)
			run("send-keys", "-t", other, "printf '\\007'", "Enter")
			wait(func() bool { return run("display-message", "-p", "-t", other, "#{window_bell_flag}") == "1" })
			attached.write(t, "\x1b[I")
			wait(func() bool { return run("show-options", "-wqv", "-t", session+":main", "@tdev_unfocused_bell") == "0" })
			title("[!] " + baseTitle)
			run("select-window", "-t", other)
			title(baseTitle)
			if !remote {
				// A scratch created inside the attached tmux switches that client.
				// The outer filter must follow server project identity without
				// sending its local connection name back to the server.
				client := run("list-clients", "-t", session, "-F", "#{client_name}")
				command := tmux.Quote(binary) + " --config-dir " + tmux.Quote(root) + " scratch local --name title-scratch"
				run("send-keys", "-t", other, command, "Enter")
				title("tdev: local/title-scratch")
				run("switch-client", "-c", client, "-t", session)
				title(baseTitle)
			}
			var second *terminalProcess
			if remote {
				// Two client aliases for the same remote session retain independent
				// titles; only tmux state is shared.
				second = terminalCLI(t, binary, root, sshConfig, "open", "other-connection/demo")
				second.waitText(t, "\x1b]2;tdev: other-connection/demo\a")
				run("send-keys", "-t", session+":main", "printf '\\007'", "Enter")
				title("[!] " + baseTitle)
				second.waitText(t, "\x1b]2;[!] tdev: other-connection/demo\a")
				run("select-window", "-t", session+":main")
				title(baseTitle)
			}
			run("detach-client", "-s", session)
			attached.exit(t, 0)
			if second != nil {
				second.exit(t, 0)
			}
		})
	}
}
