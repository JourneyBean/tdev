package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestClientProjectManagement(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.0.3-dev")
	root, remoteRoot := t.TempDir(), t.TempDir()
	store := config.Store{Root: remoteRoot}
	directory := filepath.Join(t.TempDir(), "中文 项目' $(false)")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	// Empty setup offers project creation and validates the remote directory.
	wizard := terminalCLI(t, binary, root, s.Config, "setup", "--interactive", "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	wizard.waitText(t, "输入序号")
	wizard.write(t, "1\r")
	wizard.waitText(t, "给服务器起个名字")
	wizard.write(t, "fixture\r")
	wizard.waitText(t, "现在添加第一个项目")
	wizard.write(t, "y\r")
	wizard.waitText(t, "项目名称（")
	wizard.write(t, "my-awsome-project\r")
	wizard.waitText(t, "远端已有项目目录（")
	wizard.write(t, "/nonexistent-tdev-test-project\r")
	wizard.waitText(t, "remote directory unavailable")
	wizard.write(t, directory+"\r")
	wizard.waitText(t, "已创建项目 fixture/my-awsome-project")
	wizard.exit(t, 0)
	p, err := store.Get("my-awsome-project")
	if err != nil || p.Directory != directory {
		t.Fatal("project not saved remotely", p, err)
	}
	if _, err = os.Stat(filepath.Join(root, "projects")); !os.IsNotExist(err) {
		t.Fatal("client cached remote projects", err)
	}
	run := func(input string, exit int, args ...string) {
		t.Helper()
		executeBinary(t, binary, root, s.Config, input, exit, args...)
	}
	run("", 0, "project", "list", "--server", "fixture")
	run("", 0, "ls", "--server", "fixture")
	run("", 2, "project", "list", "--server", "missing")
	run("", 2, "create", "noninteractive")
	run("", 0, "create", "fixture/second", "--directory", directory)
	run("", 4, "project", "create", "fixture/second", "--directory", directory)
	run("", 0, "project", "window", "add", "second", "--id", "api", "--name", "接口🙂", "--command", "printf '你好\\n'")
	p, err = store.Get("second")
	if err != nil || p.Windows[1].Name != "接口🙂" {
		t.Fatal(p, err)
	}
	run("", 4, "project", "window", "remove", "second", "--id", "api", "--expected-revision", "1")
	window := `{"id":"api","name":"后端","directory":".","command":"python manage.py runserver"}`
	run(window, 0, "project", "window", "update", "second", "--id", "api", "--stdin")
	run("", 0, "project", "forward", "add", "second", "-L", "8080:localhost:8000", "-R", "9000:localhost:3000")
	p, err = store.Get("second")
	if err != nil || len(p.Forwards) != 2 || p.Forwards[1].Direction != "R" {
		t.Fatal(p, err)
	}
	run("", 0, "override", "second", "--forward", p.Forwards[0].ID, "--local-port", "8081")
	run("", 0, "project", "show", "second")
	newDirectory := t.TempDir()
	run("", 0, "project", "update", "second", "--directory", newDirectory)
	p, _ = store.Get("second")
	if p.Directory != newDirectory || p.Forwards[0].ListenPort != 8080 {
		t.Fatal("update wrote client override", p)
	}
	p.Windows[1].Name = "新窗口"
	encoded, _ := json.Marshal(p)
	run(string(encoded), 0, "project", "update", "second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	run(string(encoded), 4, "project", "update", "second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	run("", 0, "project", "forward", "remove", "second", "--id", p.Forwards[1].ID)
	run("", 0, "project", "window", "remove", "second", "--id", "api")
	// The standalone wizard also targets an explicitly selected saved server.
	create := terminalCLI(t, binary, root, s.Config, "create", "--server", "fixture")
	create.waitText(t, "项目名称（")
	create.write(t, "third\r")
	create.waitText(t, "远端已有项目目录（")
	create.write(t, directory+"\r")
	create.waitText(t, "已创建项目 fixture/third")
	create.exit(t, 0)
	t.Run("open_guides_missing_project", func(t *testing.T) {
		declined := terminalCLI(t, binary, root, s.Config, "open", "declined")
		declined.waitText(t, "现在配置并打开这个项目")
		declined.write(t, "n\r")
		declined.exit(t, 3)
		if _, err := store.Get("declined"); err == nil {
			t.Fatal("declined creation wrote a project")
		}
		cancelled := terminalCLI(t, binary, root, s.Config, "open", "cancelled")
		cancelled.waitText(t, "现在配置并打开这个项目")
		cancelled.write(t, "y\r")
		cancelled.waitText(t, "远端已有项目目录（")
		cancelled.write(t, "\x03")
		cancelled.exit(t, 130)
		if _, err := store.Get("cancelled"); err == nil {
			t.Fatal("cancelled wizard wrote a project")
		}
		run("", 2, "open", "json-missing")
		if _, err := store.Get("json-missing"); err == nil {
			t.Fatal("JSON open created a project")
		}
		opened := terminalCLI(t, binary, root, s.Config, "open", "fixture/from-open")
		opened.waitText(t, "现在配置并打开这个项目")
		opened.write(t, "y\r")
		opened.waitText(t, "远端已有项目目录（")
		opened.write(t, directory+"\r")
		opened.waitText(t, "正在打开工作区")
		opened.waitText(t, "shell*")
		opened.write(t, "printf 'OPEN_CREATED_READY\\n'\r")
		opened.waitText(t, "OPEN_CREATED_READY\r\n")
		opened.write(t, "tmux detach-client\r")
		opened.exit(t, 0)
		p, err := store.Get("from-open")
		if err != nil || p.Directory != directory {
			t.Fatal("open did not save remote project", p, err)
		}
		run("", 0, "stop", "from-open", "-y")
	})
	// Definition edits do not start any tmux sessions or execute startup commands.
	for _, name := range []string{"second", "third", "my-awsome-project"} {
		project, _ := store.Get(name)
		status, err := (tmux.Manager{Store: store, Socket: s.Socket}).Status(context.Background(), project)
		if err != nil || status.Status != "missing" {
			t.Fatal("management started a session", status, err)
		}
	}
	clientConfig, _ := os.ReadFile(filepath.Join(root, "client.yaml"))
	if strings.Contains(string(clientConfig), directory) {
		t.Fatal("remote path cached on client")
	}
	t.Run("choose_server_before_creating", func(t *testing.T) {
		otherRoot := t.TempDir()
		executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "other", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", otherRoot)
		choose := terminalCLI(t, binary, root, s.Config, "create")
		choose.waitText(t, "选择项目所在服务器")
		choose.write(t, "missing\r")
		choose.waitText(t, "请选择列表中的服务器")
		choose.write(t, "1\r")
		choose.waitText(t, "项目名称（")
		choose.write(t, "selected\r")
		choose.waitText(t, "远端已有项目目录（")
		choose.write(t, directory+"\r")
		choose.waitText(t, "已创建项目 fixture/selected")
		choose.exit(t, 0)
		if _, err := store.Get("selected"); err != nil {
			t.Fatal(err)
		}
		if _, err := (config.Store{Root: otherRoot}).Get("selected"); err == nil {
			t.Fatal("created on default server instead of selected server")
		}
		bare := terminalCLI(t, binary, root, s.Config, "create", "named")
		bare.waitText(t, "选择项目所在服务器")
		bare.write(t, "other\r")
		bare.waitText(t, "远端已有项目目录（")
		bare.write(t, directory+"\r")
		bare.waitText(t, "已创建项目 other/named")
		bare.exit(t, 0)
		run("", 2, "create", "unqualified", "--directory", directory)
		run("", 0, "create", "flagged", "--server", "fixture", "--directory", directory)
		cancel := terminalCLI(t, binary, root, s.Config, "create")
		cancel.waitText(t, "选择项目所在服务器")
		cancel.write(t, "q\r")
		cancel.exit(t, 130)
		a := &app{store: config.Store{Root: root}}
		prefs, err := a.prefs()
		if err != nil || prefs.DefaultServer != "other" {
			t.Fatal("selection changed default server", err)
		}
	})
}
