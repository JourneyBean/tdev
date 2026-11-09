package tmux

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type alertOutput struct {
	sync.Mutex
	buffer bytes.Buffer
}

func (b *alertOutput) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.buffer.Write(p)
}

func (b *alertOutput) lastTitle() string {
	b.Lock()
	defer b.Unlock()
	// xterm's title capability uses OSC 0, terminated by BEL.
	parts := strings.Split(b.buffer.String(), "\x1b]0;")
	if len(parts) < 2 {
		return ""
	}
	title, _, complete := strings.Cut(parts[len(parts)-1], "\a")
	if !complete {
		return ""
	}
	return title
}

func waitAlert(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("tmux alert state did not converge")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAlertTitlesAndFocus(t *testing.T) {
	m, p, ctx := fixture(t)
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := m.run(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := run("display-message", "-p", "-t", s.SessionID, "#{window_id}")
	b := run("new-window", "-d", "-P", "-F", "#{window_id}", "-t", s.SessionID+":", "-n", "agent-b")
	run("set-option", "-t", s.SessionID, "status-interval", "60")
	cmd := exec.CommandContext(ctx, "tmux", m.Args("attach-session", "-t", s.SessionID)...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	var output alertOutput
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(&output, terminal); close(drained) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = terminal.Close()
		<-drained
	})
	title := func(window string, pending bool) {
		t.Helper()
		name := run("display-message", "-p", "-t", window, "#W")
		want := s.SessionName + ":" + name
		if pending {
			want = "[!] " + want
		}
		waitAlert(t, func() bool { return output.lastTitle() == want })
	}
	focus := func(in bool) {
		t.Helper()
		sequence, value := "\x1b[O", "0"
		if in {
			sequence, value = "\x1b[I", "1"
		}
		if _, err := terminal.WriteString(sequence); err != nil {
			t.Fatal(err)
		}
		waitAlert(t, func() bool {
			return run("show-options", "-qv", "-t", s.SessionID, "@tdev_terminal_focused") == value
		})
	}
	bell := func(window string) {
		t.Helper()
		run("send-keys", "-t", window, "printf '\\007'", "Enter")
	}
	flag := func(window, want string) {
		t.Helper()
		waitAlert(t, func() bool {
			return run("show-options", "-wqv", "-t", window, "@tdev_unfocused_bell") == want
		})
	}
	title(a, false)
	bell(b)
	title(a, true)
	focus(false)
	bell(a)
	flag(a, "1")
	title(a, true)
	focus(true)
	flag(a, "0")
	title(a, true) // The background window's native alert remains.
	if run("display-message", "-p", "-t", b, "#{window_bell_flag}") != "1" {
		t.Fatal("focus-in cleared another window's native bell")
	}
	run("select-window", "-t", b)
	title(b, false)
	focus(false)
	bell(b)
	flag(b, "1")
	title(b, true) // A user option must cause immediate OSC refresh.
	run("select-window", "-t", a)
	title(a, true) // Custom flags in other windows must also aggregate.
	focus(true)
	title(a, true)
	flag(b, "1")
	run("select-window", "-t", b)
	flag(b, "0")
	title(b, false)
	// A focused current-window bell must remain acknowledged by tmux.
	bell(b)
	run("send-keys", "-t", b, "printf 'BELL_PROCESSED\\n'", "Enter")
	waitAlert(t, func() bool {
		return strings.Contains(run("capture-pane", "-p", "-t", b), "BELL_PROCESSED\n")
	})
	flag(b, "0")
	title(b, false)
}

func TestAlertConfigurationPreservesHooksAndOtherSessions(t *testing.T) {
	m, p, ctx := fixture(t)
	run := func(args ...string) string {
		t.Helper()
		out, err := m.run(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	other := run("new-session", "-d", "-P", "-F", "#{session_id}", "-s", "unmanaged")
	run("set-hook", "-g", "alert-bell[7]", "set-option @user_bell preserved")
	run("set-option", "-g", "set-titles", "off")
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	hooks := run("show-hooks", "-t", s.SessionID, "alert-bell")
	if !strings.Contains(hooks, "alert-bell[7] set-option @user_bell preserved") {
		t.Fatal("inherited user hook lost", hooks)
	}
	if err := m.configureAlerts(ctx, s.SessionID); err != nil {
		t.Fatal(err)
	}
	if again := run("show-hooks", "-t", s.SessionID, "alert-bell"); again != hooks {
		t.Fatal("configuration duplicated hooks", again)
	}
	if run("show-options", "-gv", "set-titles") != "off" || run("show-options", "-t", other, "set-titles") != "" {
		t.Fatal("unmanaged title configuration changed")
	}
	if run("show-hooks", "-g", "alert-bell") != "alert-bell[7] set-option @user_bell preserved" {
		t.Fatal("global hook configuration changed")
	}
	scratch, err := m.CreateScratch(ctx, "scratch", p.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if run("show-options", "-v", "-t", scratch.SessionID, "set-titles") != "on" {
		t.Fatal("scratch has no title configuration")
	}
}
