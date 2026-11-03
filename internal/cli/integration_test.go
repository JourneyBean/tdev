package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
	"tdev/internal/update"
)

func buildCLI(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tdev")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-ldflags", "-X tdev/internal/cli.Version="+version, "-o", path, "../../cmd/tdev")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, b)
	}
	return path
}
func executeBinary(t *testing.T, binary, root, sshConfig, input string, want int, args ...string) protocol.Response {
	t.Helper()
	argv := append([]string{"--config-dir", root, "--ssh-config", sshConfig, "--json"}, args...)
	cmd := exec.Command(binary, argv...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if code != want {
		t.Fatalf("%v exit=%d want=%d stdout=%s stderr=%s", args, code, want, stdout.String(), stderr.String())
	}
	var r protocol.Response
	if err = json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("not one JSON response: %q (%s)", stdout.String(), stderr.String())
	}
	return r
}
func signedRelease(t *testing.T, binary, version, keyPath string) string {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if err = os.WriteFile(filepath.Join(dir, "tdev-"+goos+"-"+arch), data, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = update.BuildRelease(dir, version, keyPath); err != nil {
		t.Fatal(err)
	}
	return dir
}

type terminalProcess struct {
	cmd      *exec.Cmd
	terminal *os.File
	output   sshx.Buffer
	done     chan error
}

func terminalCLI(t *testing.T, binary, root, sshConfig string, args ...string) *terminalProcess {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"--config-dir", root, "--ssh-config", sshConfig}, args...)...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	return terminalCommand(t, cmd)
}

func terminalCommand(t *testing.T, cmd *exec.Cmd) *terminalProcess {
	t.Helper()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	p := &terminalProcess{cmd: cmd, terminal: f, done: make(chan error, 1)}
	go func() { _, _ = io.Copy(&p.output, f) }()
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = f.Close() })
	return p
}
func (p *terminalProcess) write(t *testing.T, s string) {
	t.Helper()
	if _, err := p.terminal.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
func (p *terminalProcess) waitText(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for {
		if strings.Contains(p.output.String(), text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal missing %q: %q", text, p.output.String())
		}
		time.Sleep(15 * time.Millisecond)
	}
}
func (p *terminalProcess) exit(t *testing.T, want int) {
	t.Helper()
	select {
	case err := <-p.done:
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want {
			t.Fatalf("terminal exit=%d want=%d output=%q", code, want, p.output.String())
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("terminal did not exit: %q", p.output.String())
	}
}

func TestFirstVersionSSHWorkflow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated sshd acceptance runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0-dev")
	root := t.TempDir()
	remoteRoot := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "项目_日本語_한국어_🙂")
	if err := os.Mkdir(projectDir, 0700); err != nil {
		t.Fatal(err)
	}
	store := config.Store{Root: remoteRoot}
	project, err := store.Create(context.Background(), "demo", projectDir)
	if err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	a := &app{store: config.Store{Root: root}, sshConfig: s.Config, timeout: 5 * time.Second}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	prefs.SSH.ConnectTimeout = time.Second
	prefs.SSH.ServerAliveInterval = time.Second
	prefs.SSH.ServerAliveCountMax = 1
	prefs.Reconnect.Terminal.Attempts = 1
	prefs.Reconnect.Terminal.Interval = 100 * time.Millisecond
	data, _ := yaml.Marshal(prefs)
	if err = config.AtomicWrite(filepath.Join(root, "client.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("files_exact_paths_and_confirmation", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source")
		if err = os.MkdirAll(filepath.Join(source, "empty"), 0750); err != nil {
			t.Fatal(err)
		}
		name := "space [x] '中文'.txt"
		if err = os.WriteFile(filepath.Join(source, name), []byte("payload"), 0640); err != nil {
			t.Fatal(err)
		}
		executeBinary(t, binary, root, s.Config, "", 0, "push", "demo", source, "target with spaces")
		target := filepath.Join(projectDir, "target with spaces")
		if b, err := os.ReadFile(filepath.Join(target, name)); err != nil || string(b) != "payload" {
			t.Fatalf("wrong upload layer: %q %v", b, err)
		}
		if err = os.WriteFile(filepath.Join(target, "unrelated"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		executeBinary(t, binary, root, s.Config, "no\n", 130, "push", "demo", source, "target with spaces")
		executeBinary(t, binary, root, s.Config, "", 0, "push", "demo", source, "target with spaces", "-y")
		local := filepath.Join(t.TempDir(), "exact pull")
		executeBinary(t, binary, root, s.Config, "", 0, "pull", "demo", "target with spaces", local)
		if b, err := os.ReadFile(filepath.Join(local, name)); err != nil || string(b) != "payload" {
			t.Fatalf("wrong download layer %q %v", b, err)
		}
		if b, _ := os.ReadFile(filepath.Join(target, "unrelated")); string(b) != "keep" {
			t.Fatal("deleted unrelated remote file")
		}
	})
	t.Run("native_terminal_ctrl_c_resize_and_two_outages", func(t *testing.T) {
		p := terminalCLI(t, binary, root, s.Config, "open", "demo")
		p.write(t, "printf 'TDEV_READY_ONE\\n'\r")
		p.waitText(t, "TDEV_READY_ONE\r\n")
		p.write(t, "printf 'TDEV_CJK_中文_日本語_한글_🙂\\n'\r")
		p.waitText(t, "TDEV_CJK_中文_日本語_한글_🙂\r\n")
		time.Sleep(1200 * time.Millisecond)
		for n := 1; n <= 2; n++ {
			before := len(p.output.String())
			s.Offline(true)
			deadline := time.Now().Add(8 * time.Second)
			for !strings.Contains(p.output.String()[min(before, len(p.output.String())):], "Connection lost.") {
				if time.Now().After(deadline) {
					t.Fatalf("disconnect not detected: %q", p.output.String())
				}
				time.Sleep(15 * time.Millisecond)
			}
			s.Offline(false)
			marker := fmt.Sprintf("TDEV_RESTORED_%d", n)
			p.write(t, "printf '"+marker+"\\n'\r")
			p.waitText(t, marker+"\r\n")
			p.write(t, "printf 'TDEV_CJK_重连_日本語_한글\\n'\r")
			p.waitText(t, "TDEV_CJK_重连_日本語_한글\r\n")
			time.Sleep(1200 * time.Millisecond)
		}
		p.write(t, "sleep 30\r")
		time.Sleep(100 * time.Millisecond)
		p.write(t, "\x03")
		p.write(t, "printf 'TDEV_STILL_ATTACHED\\n'\r")
		p.waitText(t, "TDEV_STILL_ATTACHED\r\n")
		if err = pty.Setsize(p.terminal, &pty.Winsize{Rows: 40, Cols: 110}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		p.write(t, "stty size\r")
		p.waitText(t, "39 110")
		p.write(t, "tmux detach-client\r")
		p.exit(t, 0)
	})
	t.Run("cancel_waiting_and_no_recreate_after_stop", func(t *testing.T) {
		p := terminalCLI(t, binary, root, s.Config, "open", "demo", "--reconnect-attempts", "always")
		p.write(t, "printf 'TDEV_CANCEL_READY\\n'\r")
		p.waitText(t, "TDEV_CANCEL_READY\r\n")
		s.Offline(true)
		p.waitText(t, "Connection lost.")
		p.write(t, "\x03")
		p.exit(t, 130)
		s.Offline(false)
		p = terminalCLI(t, binary, root, s.Config, "open", "demo", "--reconnect-attempts", "always")
		p.write(t, "printf 'TDEV_STOP_READY\\n'\r")
		p.waitText(t, "TDEV_STOP_READY\r\n")
		s.Offline(true)
		p.waitText(t, "Connection lost.")
		manager := tmux.Manager{Store: store, Socket: s.Socket}
		if _, err = manager.Stop(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		s.Offline(false)
		p.exit(t, 5)
		status, err := manager.Status(context.Background(), project)
		if err != nil || status.Status != "missing" {
			t.Fatalf("recreated stopped session %+v %v", status, err)
		}
	})
	t.Run("signed_bootstrap_upgrade_remote_and_rollback", func(t *testing.T) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			t.Fatal(err)
		}
		key := filepath.Join(t.TempDir(), "fixture-only.pem")
		if err = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		v1 := signedRelease(t, binary, "0.1.0-dev", key)
		t.Run("wizard_installs_missing_remote_and_discovers_projects", func(t *testing.T) {
			clientRoot := t.TempDir()
			destination := filepath.Join(t.TempDir(), "remote", "tdev")
			before, err := os.ReadFile(filepath.Join(remoteRoot, "projects", "demo.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			wizard := terminalCLI(t, binary, clientRoot, s.Config, "setup", "--interactive", "--remote-binary", destination, "--remote-config-dir", remoteRoot, "--release-dir", v1, "--public-key", filepath.Join(v1, "release.pub"))
			wizard.waitText(t, "输入序号")
			wizard.write(t, "1\r")
			wizard.waitText(t, "给服务器起个名字")
			wizard.write(t, "\r")
			wizard.waitText(t, "安装官方签名版本到用户目录")
			wizard.write(t, "y\r")
			wizard.waitText(t, "已保存连接 fixture")
			wizard.exit(t, 0)
			after, err := os.ReadFile(filepath.Join(remoteRoot, "projects", "demo.yaml"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("setup altered existing remote project", err)
			}
			if err = update.Candidate(context.Background(), destination, "0.1.0-dev"); err != nil {
				t.Fatal(err)
			}
		})
		template, err := os.ReadFile("../../scripts/install.sh")
		if err != nil {
			t.Fatal(err)
		}
		if err = update.BuildInstaller(v1, string(template)); err != nil {
			t.Fatal(err)
		}
		bootstrapHome := filepath.Join(t.TempDir(), "用户 home")
		if err = os.Mkdir(bootstrapHome, 0700); err != nil {
			t.Fatal(err)
		}
		bootstrap := exec.Command("sh", filepath.Join(v1, "install.sh"), "--release-dir", v1, "--shell", "bash", "--completion", "all", "--config-dir", filepath.Join(bootstrapHome, "config"))
		bootstrap.Env = append(os.Environ(), "HOME="+bootstrapHome, "ZDOTDIR="+bootstrapHome, "OPENSSL=/nonexistent/openssl")
		if output, err := bootstrap.CombinedOutput(); err != nil {
			t.Fatalf("pinned bootstrap without OpenSSL failed: %v %s", err, output)
		}
		installed := filepath.Join(bootstrapHome, ".local/bin/tdev")
		for _, name := range []string{".bashrc", ".profile", ".zshrc"} {
			content, err := os.ReadFile(filepath.Join(bootstrapHome, name))
			if err != nil || !strings.Contains(string(content), "# >>> tdev completion >>>") {
				t.Fatalf("bootstrap completion missing: %s %v", name, err)
			}
		}
		t.Run("pipe_installer_completion_prompt", func(t *testing.T) {
			for _, answer := range []string{"y", "n"} {
				home := t.TempDir()
				cmd := exec.Command("sh", "-c", `script=$1; shift; cat "$script" | sh -s -- "$@"`, "sh", filepath.Join(v1, "install.sh"), "--release-dir", v1, "--shell", "zsh", "--config-dir", filepath.Join(home, "config"))
				cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "TERM=xterm-256color")
				installer := terminalCommand(t, cmd)
				installer.waitText(t, "Configure zsh command completion?")
				installer.write(t, answer+"\r")
				installer.exit(t, 0)
				content, err := os.ReadFile(filepath.Join(home, ".zshrc"))
				if err != nil || strings.Contains(string(content), "# >>> tdev completion >>>") != (answer == "y") {
					t.Fatalf("installer completion choice %s: %v", answer, err)
				}
			}
		})
		t.Run("binary_install_completion", func(t *testing.T) {
			for _, mode := range []string{"all", "auto"} {
				home := t.TempDir()
				args := []string{"--config-dir", filepath.Join(home, "config"), "install", "--release-dir", v1, "--public-key", filepath.Join(v1, "release.pub"), "--destination", filepath.Join(home, ".local/bin/tdev"), "--completion", mode}
				cmd := exec.Command(binary, args...)
				cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "SHELL=/bin/zsh", "TERM=xterm-256color")
				if mode == "auto" {
					installer := terminalCommand(t, cmd)
					installer.waitText(t, "Configure zsh command completion?")
					installer.write(t, "y\r")
					installer.exit(t, 0)
				} else if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("binary completion install: %v %s", err, output)
				}
				content, err := os.ReadFile(filepath.Join(home, ".zshrc"))
				if err != nil || !strings.Contains(string(content), "# >>> tdev completion >>>") {
					t.Fatalf("binary completion missing: %v", err)
				}
			}
		})
		if err = update.Candidate(context.Background(), installed, "0.1.0-dev"); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{".bashrc", ".profile"} {
			content, err := os.ReadFile(filepath.Join(bootstrapHome, name))
			if err != nil || !bytes.Contains(content, []byte(filepath.Dir(installed))) {
				t.Fatalf("PATH configuration missing in %s: %v", name, err)
			}
		}
		v2binary := buildCLI(t, "0.1.1-dev")
		v2 := signedRelease(t, v2binary, "0.1.1-dev", key)
		local := filepath.Join(t.TempDir(), "managed", "tdev")
		cmd := exec.Command("sh", "../../scripts/install.sh", "--release-dir", v1, "--public-key", filepath.Join(v1, "release.pub"), "--destination", local, "--config-dir", root)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bootstrap failed: %v %s", err, output)
		}
		executeBinary(t, local, root, s.Config, "", 0, "upgrade", "--release-dir", v2, "-y")
		if err = update.Candidate(context.Background(), local, "0.1.1-dev"); err != nil {
			t.Fatal(err)
		}
		executeBinary(t, local, root, s.Config, "", 0, "upgrade", "--rollback")
		if err = update.Candidate(context.Background(), local, "0.1.0-dev"); err != nil {
			t.Fatal(err)
		}
		remoteBin := filepath.Join(t.TempDir(), "remote managed", "tdev")
		executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "installed", "--ssh-alias", s.Alias, "--remote-binary", remoteBin, "--remote-config-dir", remoteRoot, "--install", "--release-dir", v1, "--public-key", filepath.Join(v1, "release.pub"), "-y")
		manager := tmux.Manager{Store: store, Socket: s.Socket}
		before, err := manager.Ensure(context.Background(), project)
		if err != nil {
			t.Fatal(err)
		}
		executeBinary(t, binary, root, s.Config, "", 0, "upgrade", "--remote", "installed", "--release-dir", v2, "-y")
		if err = update.Candidate(context.Background(), remoteBin, "0.1.1-dev"); err != nil {
			t.Fatal(err)
		}
		after, err := manager.Status(context.Background(), project)
		if err != nil || after.Generation != before.Generation {
			t.Fatalf("upgrade replaced session %+v %+v %v", before, after, err)
		}
		executeBinary(t, binary, root, s.Config, "", 0, "upgrade", "--remote", "installed", "--rollback")
		if err = update.Candidate(context.Background(), remoteBin, "0.1.0-dev"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("retry_counts_auth_host_keys_and_connect_cancel", func(t *testing.T) {
		for _, attempts := range []string{"0", "2"} {
			before := s.Accepted.Load()
			s.Offline(true)
			p := terminalCLI(t, binary, root, s.Config, "open", "fixture/demo", "--reconnect-attempts", attempts, "--reconnect-interval", "20ms")
			p.exit(t, 6)
			s.Offline(false)
			want := int64(1)
			if attempts == "2" {
				want = 3
			}
			if count := s.Accepted.Load() - before; count != want {
				t.Fatalf("initial connection retries=%s opened %d transports, expected %d: %q", attempts, count, want, p.output.String())
			}
		}
		knownPath := filepath.Join(s.Dir, "known_hosts")
		known, err := os.ReadFile(knownPath)
		if err != nil {
			t.Fatal(err)
		}
		public, err := os.ReadFile(filepath.Join(s.Dir, "client.pub"))
		if err != nil {
			t.Fatal(err)
		}
		hostFields := strings.Fields(string(known))
		keyFields := strings.Fields(string(public))
		wrong := hostFields[0] + " " + keyFields[0] + " " + keyFields[1] + "\n"
		if err = os.WriteFile(knownPath, []byte(wrong), 0600); err != nil {
			t.Fatal(err)
		}
		p := terminalCLI(t, binary, root, s.Config, "open", "fixture/demo", "--reconnect-attempts", "always")
		p.exit(t, 6)
		if err = os.WriteFile(knownPath, known, 0600); err != nil {
			t.Fatal(err)
		}
		text, err := os.ReadFile(s.Config)
		if err != nil {
			t.Fatal(err)
		}
		badKey := filepath.Join(t.TempDir(), "bad-key.conf")
		if err = os.WriteFile(badKey, []byte(strings.ReplaceAll(string(text), "/client\n", "/does-not-exist\n")), 0600); err != nil {
			t.Fatal(err)
		}
		p = terminalCLI(t, binary, root, badKey, "open", "fixture/demo", "--reconnect-attempts", "always")
		p.exit(t, 6)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		connected := make(chan net.Conn, 1)
		go func() {
			c, err := listener.Accept()
			if err == nil {
				connected <- c
			}
		}()
		blackhole := filepath.Join(t.TempDir(), "blackhole.conf")
		replacement := strings.ReplaceAll(string(text), fmt.Sprintf("Port %d", s.Port), fmt.Sprintf("Port %d", listener.Addr().(*net.TCPAddr).Port))
		if err = os.WriteFile(blackhole, []byte(replacement), 0600); err != nil {
			t.Fatal(err)
		}
		p = terminalCLI(t, binary, root, blackhole, "open", "fixture/demo", "--reconnect-attempts", "always")
		select {
		case connection := <-connected:
			defer connection.Close()
		case <-time.After(5 * time.Second):
			t.Fatal("did not enter an SSH connection attempt")
		}
		p.write(t, "\x03")
		p.exit(t, 130)
	})
}
