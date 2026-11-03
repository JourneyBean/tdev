package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/testssh"
)

func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestSetupWizardAndFreshClientOverrides(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.0.3-dev")
	root := t.TempDir()
	remoteRoot := t.TempDir()
	store := config.Store{Root: remoteRoot}
	project, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	baselinePort, localPort := freeLocalPort(t), freeLocalPort(t)
	project, err = store.Update(context.Background(), project.Name, project.Revision, func(p *config.Project) error {
		p.Forwards = []config.Forward{{ID: "api", Direction: "L", BindAddress: "127.0.0.1", ListenPort: baselinePort, TargetHost: "localhost", TargetPort: backend.Addr().(*net.TCPAddr).Port}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	wizard := terminalCLI(t, binary, root, s.Config, "setup", "--interactive")
	wizard.waitText(t, "输入序号")
	wizard.write(t, "\r")
	wizard.waitText(t, "要调整这些项目在本机使用的端口吗")
	wizard.write(t, "y\r")
	wizard.waitText(t, "的本机端口")
	wizard.write(t, fmt.Sprintf("%d\r", localPort))
	wizard.waitText(t, "已保存连接 fixture")
	wizard.exit(t, 0)
	content, err := os.ReadFile(filepath.Join(root, "client.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), project.Directory) || strings.Contains(string(content), "windows:") {
		t.Fatal("cached remote project definitions in client preferences")
	}
	if strings.Contains(string(content), "project_id:") {
		t.Fatal("stored new override fields in legacy client.yaml")
	}
	overrides, err := os.ReadFile(filepath.Join(root, "overrides.yaml"))
	if err != nil || !strings.Contains(string(overrides), project.ID) {
		t.Fatal("client override file missing", err)
	}
	project, err = store.Update(context.Background(), project.Name, project.Revision, func(p *config.Project) error { p.Windows[0].Name = "最新窗口_中文_🙂"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	response := executeBinary(t, binary, root, s.Config, "", 0, "ls")
	encoded, _ := json.Marshal(response.Data)
	var projects map[string][]discoveredProject
	if err = json.Unmarshal(encoded, &projects); err != nil {
		t.Fatal(err)
	}
	listed := projects["fixture"][0]
	if listed.Project.Windows[0].Name != "最新窗口_中文_🙂" || listed.Project.Forwards[0].ListenPort != localPort || listed.RemoteProject.Forwards[0].ListenPort != baselinePort {
		t.Fatal("ls did not merge fresh remote configuration", listed)
	}
	client := terminalCLI(t, binary, root, s.Config, "open", "demo")
	client.write(t, "printf 'OVERRIDE_WORKSPACE_READY\\n'\r")
	client.waitText(t, "OVERRIDE_WORKSPACE_READY\r\n")
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("test"))
	payload := make([]byte, 4)
	if _, err = io.ReadFull(conn, payload); err != nil || string(payload) != "test" {
		t.Fatal("open did not use local port override", err)
	}
	_ = conn.Close()
	client.write(t, "tmux detach-client\r")
	client.exit(t, 0)
	executeBinary(t, binary, root, s.Config, "", 0, "save", "demo", "-y")
	remote, err := store.Get("demo")
	if err != nil || remote.Forwards[0].ListenPort != baselinePort {
		t.Fatal("save wrote client ports to remote", err)
	}
	if _, err = store.Create(context.Background(), "new-project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	response = executeBinary(t, binary, root, s.Config, "", 0, "ls")
	encoded, _ = json.Marshal(response.Data)
	_ = json.Unmarshal(encoded, &projects)
	if len(projects["fixture"]) != 2 {
		t.Fatal("ls used a stale project list")
	}
	executeBinary(t, binary, root, s.Config, "", 2, "override", "demo", "--forward", "api", "--local-port", "65536")
	executeBinary(t, binary, root, s.Config, "", 0, "override", "demo", "--reset")
	// Add a direct address, keeping the fixture's host verification and existing SSH file intact.
	sshBytes, err := os.ReadFile(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	sshBytes = append(sshBytes, []byte("\nHost *\n UserKnownHostsFile "+filepath.Join(s.Dir, "known_hosts")+"\n StrictHostKeyChecking yes\n")...)
	if err = os.WriteFile(s.Config, sshBytes, 0600); err != nil {
		t.Fatal(err)
	}
	username, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	direct := terminalCLI(t, binary, newRoot, s.Config, "setup", "--interactive", "--remote-binary", binary, "--remote-config-dir", remoteRoot, "--identity-file", filepath.Join(s.Dir, "client"))
	direct.waitText(t, "输入序号")
	direct.write(t, "0\r")
	direct.waitText(t, "服务器地址（例如")
	direct.write(t, fmt.Sprintf("%s@127.0.0.1:%d\r", username.Username, s.Port))
	direct.waitText(t, "给服务器起个名字")
	direct.write(t, "newdev\r")
	direct.waitText(t, "要调整这些项目在本机使用的端口吗")
	direct.write(t, "n\r")
	direct.waitText(t, "已保存连接 newdev")
	direct.exit(t, 0)
	afterSSH, _ := os.ReadFile(s.Config)
	if !bytes.Equal(afterSSH, sshBytes) {
		t.Fatal("wizard rewrote existing SSH configuration")
	}
	executeBinary(t, binary, newRoot, "", "", 0, "ls")
}
