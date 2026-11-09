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
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
	"tdev/internal/update"
)

// Exercise real signed replacements while an old CLI owns a live SSH PTY and
// a leased forward. Check executable identities as well as successful probes.
func TestUpgradeWithActiveSSHAndForwards(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd and executable identity checks require Linux")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := testssh.Start(t)
	oldBinary, newBinary := buildCLI(t, "0.4.1"), buildCLI(t, "0.4.2")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "test-signing.pem")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	v1, v2 := signedRelease(t, oldBinary, "0.4.1", key), signedRelease(t, newBinary, "0.4.2", key)
	root, remoteRoot := t.TempDir(), t.TempDir()
	local, remote := filepath.Join(t.TempDir(), "tdev"), filepath.Join(t.TempDir(), "tdev")
	executeBinary(t, oldBinary, root, s.Config, "", 0, "install", "--release-dir", v1, "--public-key", filepath.Join(v1, "release.pub"), "--destination", local, "--completion", "none", "-y")
	executeBinary(t, local, root, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", remote, "--remote-config-dir", remoteRoot, "--install", "--release-dir", v1, "--public-key", filepath.Join(v1, "release.pub"), "-y")
	a := &app{store: config.Store{Root: root}}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	prefs.SSH.ConnectTimeout = time.Second
	prefs.SSH.ServerAliveInterval = time.Second
	prefs.SSH.ServerAliveCountMax = 1
	prefs.Reconnect.Terminal.Attempts = 30
	prefs.Reconnect.Terminal.Interval = 100 * time.Millisecond
	prefs.Reconnect.Forward.Attempts = 30
	prefs.Reconnect.Forward.Interval = 100 * time.Millisecond
	data, _ := yaml.Marshal(prefs)
	if err := config.AtomicWrite(filepath.Join(root, "client.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Reload the connection settings before opening a terminal.
	executeBinary(t, local, root, s.Config, "", 0, "daemon", "restart")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	reservation.Close()
	m := tmux.Manager{Store: config.Store{Root: remoteRoot}, Socket: s.Socket}
	p, err := m.Store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", port, listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	p, err = m.Store.Update(context.Background(), p.Name, p.Revision, func(p *config.Project) error { p.Forwards = []config.Forward{f}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	terminal := terminalCLI(t, local, root, s.Config, "open", "fixture/demo")
	terminal.write(t, "printf 'UPGRADE_READY\\n'\r")
	terminal.waitText(t, "UPGRADE_READY\r\n")
	original, err := m.Status(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	waitFor := func(description string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				client, _ := daemon.New(root)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				state, err := client.Call(ctx, daemon.Request{Op: "status"})
				cancel()
				t.Logf("daemon state on timeout: %+v; error=%v", state, err)
				t.Fatalf("%s: %q", description, terminal.output.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	roundtrip := func(port int) bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			return false
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := conn.Write([]byte("upgrade-echo")); err != nil {
			return false
		}
		b := make([]byte, len("upgrade-echo"))
		_, err = io.ReadFull(conn, b)
		return err == nil && string(b) == "upgrade-echo"
	}
	forwardWorks := func() bool { return roundtrip(port) }
	image := func(pid int) os.FileInfo {
		t.Helper()
		info, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	attachedClientPID := func() string {
		out, err := exec.Command("tmux", m.Args("list-clients", "-t", original.SessionID, "-F", "#{client_pid}")...).Output()
		if err != nil || len(strings.Fields(string(out))) != 1 {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	// tmux's attached client is a child of the actual remote tdev API process.
	remoteAttachmentPIDs := map[int]bool{}
	attachedImage := func() os.FileInfo {
		pid := attachedClientPID()
		if pid == "" {
			return nil
		}
		status, err := os.ReadFile("/proc/" + pid + "/status")
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				pid, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
				executable := fmt.Sprintf("/proc/%d/exe", pid)
				path, err := os.Readlink(executable)
				if err != nil || strings.TrimSuffix(path, " (deleted)") != remote {
					return nil
				}
				info, _ := os.Stat(executable)
				if info != nil {
					remoteAttachmentPIDs[pid] = true
				}
				return info
			}
		}
		return nil
	}
	client, err := daemon.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backgroundPort := reservation.Addr().(*net.TCPAddr).Port
	reservation.Close()
	background, err := config.ParseForward("R", fmt.Sprintf("%d:%d", backgroundPort, listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	backgroundSSH := prefs.SSH
	backgroundSSH.ConfigFile = s.Config
	// The leased mapping uses -L; an explicit background -R exercises remote
	// listener restoration across both signed replacements.
	if _, err := client.Call(context.Background(), daemon.Request{Op: "forward", Alias: s.Alias, SSH: backgroundSSH, Forwards: []config.Forward{background}, Policy: prefs.Reconnect.Forward}); err != nil {
		t.Fatal(err)
	}
	daemonPID := func() int {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		state, err := client.Call(ctx, daemon.Request{Op: "status"})
		if err != nil {
			t.Fatal(err)
		}
		return state.PID
	}
	runUpgrade := func(want int, input string, args ...string) string {
		t.Helper()
		argv := append([]string{"--config-dir", root, "--ssh-config", s.Config, "--json", "upgrade"}, args...)
		cmd := exec.Command(local, argv...)
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		var response protocol.Response
		if code != want || json.Unmarshal(stdout.Bytes(), &response) != nil || response.OK != (want == 0) {
			t.Fatalf("upgrade %v: exit=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
		if want == 2 && (response.Error == nil || response.Error.Code != "VERIFICATION_FAILED") {
			t.Fatalf("expected signature rejection: %s", stdout.String())
		}
		if !strings.Contains(stderr.String(), "端口映射可能短暂中断") {
			t.Fatalf("missing upgrade notice with --json/-y: %s", stderr.String())
		}
		return stderr.String()
	}
	assertSession := func() {
		t.Helper()
		current, err := m.Status(context.Background(), p)
		if err != nil || current.SessionID != original.SessionID || current.Generation != original.Generation {
			t.Fatalf("upgrade changed original session: %+v %+v %v", original, current, err)
		}
	}
	waitFor("initial forward echo", forwardWorks)
	// Invalid signed content must leave the running daemon and installed bytes intact.
	beforeFailure, beforeImage := daemonPID(), image(daemonPID())
	signaturePath := filepath.Join(v2, "manifest.sig")
	signature, err := os.ReadFile(signaturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, []byte("invalid signature"), 0600); err != nil {
		t.Fatal(err)
	}
	runUpgrade(2, "", "--release-dir", v2, "-y")
	if daemonPID() != beforeFailure || !os.SameFile(image(beforeFailure), beforeImage) {
		t.Fatal("rejected upgrade disturbed daemon")
	}
	if err := update.Candidate(context.Background(), local, "0.4.1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, signature, 0600); err != nil {
		t.Fatal(err)
	}
	for _, rollback := range []bool{false, true} {
		t.Logf("remote rollback=%v", rollback)
		{
			oldImage, pid := attachedImage(), daemonPID()
			args := []string{"--remote", "fixture", "-y", "--release-dir", v2}
			version := "0.4.2"
			if rollback {
				args, version = []string{"--remote", "fixture", "--rollback"}, "0.4.1"
			}
			notice := runUpgrade(0, "", args...)
			if !strings.Contains(notice, "tdev open fixture/<项目名>") || !strings.Contains(notice, "协议不兼容") {
				t.Fatal("missing remote reconnect instructions", notice)
			}
			if err := update.Candidate(context.Background(), remote, version); err != nil {
				t.Fatal(err)
			}
			if daemonPID() != pid || oldImage == nil || attachedImage() == nil || !os.SameFile(oldImage, attachedImage()) {
				t.Fatal("remote replacement disturbed the existing daemon or attachment")
			}
			waitFor("forward after remote replacement", forwardWorks)
			assertSession()
			offset := len(terminal.output.String())
			s.Offline(true)
			waitFor("disconnect detected after remote replacement", func() bool { return strings.Contains(terminal.output.String()[offset:], "Connection lost.") })
			s.Offline(false)
			defer s.Offline(false)
			expected, err := os.Stat(remote)
			if err != nil {
				t.Fatal(err)
			}
			waitFor("reattach executes installed remote binary", func() bool {
				actual := attachedImage()
				return actual != nil && os.SameFile(actual, expected)
			})
			marker := fmt.Sprintf("REMOTE_RECONNECTED_%v", rollback)
			terminal.write(t, "printf '"+marker+"\\n'\r")
			terminal.waitText(t, marker+"\r\n")
			waitFor("forward echo after remote reconnect", forwardWorks)
			waitFor("background forward after remote reconnect", func() bool { return roundtrip(backgroundPort) })
			assertSession()
		}
	}
	for _, rollback := range []bool{false, true} {
		t.Logf("local rollback=%v", rollback)
		{
			before := daemonPID()
			previousAttachment := attachedClientPID()
			args := []string{"--release-dir", v2, "-y"}
			version := "0.4.2"
			if rollback {
				args, version = []string{"--rollback"}, "0.4.1"
			}
			if !rollback {
				notice := runUpgrade(130, "n\n", "--release-dir", v2)
				if strings.Index(notice, "端口映射可能短暂中断") > strings.Index(notice, "[y/N]") {
					t.Fatal("notice appeared after confirmation")
				}
				if daemonPID() != before {
					t.Fatal("cancelled upgrade restarted daemon")
				}
			}
			runUpgrade(0, "", args...)
			if err := update.Candidate(context.Background(), local, version); err != nil {
				t.Fatal(err)
			}
			expected, err := os.Stat(local)
			if err != nil {
				t.Fatal(err)
			}
			after := daemonPID()
			if after == before || !os.SameFile(image(after), expected) {
				t.Fatal("daemon did not move to installed executable")
			}
			if !rollback {
				runUpgrade(0, "", "--release-dir", v2, "-y")
				if daemonPID() != after {
					t.Fatal("identical upgrade restarted daemon")
				}
			}
			waitFor("forward restored after local replacement", forwardWorks)
			waitFor("background forward restored after local replacement", func() bool { return roundtrip(backgroundPort) })
			waitFor("terminal reattached after local replacement", func() bool {
				pid := attachedClientPID()
				return pid != "" && pid != previousAttachment
			})
			if attachedImage() == nil {
				t.Fatal("reattached remote process is missing")
			}
			marker := fmt.Sprintf("LOCAL_RECONNECTED_%v", rollback)
			terminal.write(t, "printf '"+marker+"\\n'\r")
			terminal.waitText(t, marker+"\r\n")
			assertSession()
		}
	}
	// A future incompatible remote must stop reconnecting with actionable local
	// upgrade guidance, leaving the original session running and executing no attach.
	remoteBytes, err := os.ReadFile(remote)
	if err != nil {
		t.Fatal(err)
	}
	invoked := filepath.Join(t.TempDir(), "unexpected-operation")
	incompatible := "#!/bin/sh\ncase \" $* \" in\n*' --json version '*) printf '%s\\n' '{\"protocol_version\":1,\"ok\":true,\"data\":{\"version\":\"99.0.0\"}}'; exit 0;;\n*' api version '*) printf '%s\\n' '{\"protocol_version\":1,\"ok\":false,\"error\":{\"code\":\"PROTOCOL_INCOMPATIBLE\",\"message\":\"client upgrade required\"}}'; exit 8;;\nesac\ntouch " + tmux.Quote(invoked) + "\nexit 1\n"
	if err := config.AtomicWrite(remote, []byte(incompatible), 0700); err != nil {
		t.Fatal(err)
	}
	s.Offline(true)
	s.Offline(false)
	terminal.exit(t, 8)
	// SSH reports channel termination before the old remote API has necessarily
	// finished its cancellation diagnostics. Wait before TempDir cleanup.
	waitFor("remote attachment processes exited", func() bool {
		for pid := range remoteAttachmentPIDs {
			if _, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid)); !os.IsNotExist(err) {
				return false
			}
		}
		return true
	})
	if output := terminal.output.String(); !strings.Contains(output, "update the local client (tdev upgrade)") || !strings.Contains(output, "tdev open fixture/<project>") || !strings.Contains(output, "99.0.0") {
		t.Fatalf("incompatible reconnect lacks recovery instructions: %q", output)
	}
	if _, err := os.Stat(invoked); !os.IsNotExist(err) {
		t.Fatal("incompatible reconnect executed an operation after the API gate", err)
	}
	assertSession()
	if err := config.AtomicWrite(remote, remoteBytes, 0755); err != nil {
		t.Fatal(err)
	}
	waitFor("last lease released after upgrade", func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
		}
		return err != nil
	})
	waitFor("background service recovered after last lease release", func() bool { return roundtrip(backgroundPort) })
	executeBinary(t, local, root, s.Config, "", 0, "daemon", "stop")
	runUpgrade(0, "", "--release-dir", v2, "-y")
	if _, err := client.Call(context.Background(), daemon.Request{Op: "status"}); err == nil {
		t.Fatal("upgrade started a deliberately stopped daemon")
	}
	if _, err := os.Stat(filepath.Join(client.Dir, "stopped")); err != nil {
		t.Fatal("upgrade removed stop marker", err)
	}
}
