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
	binary := buildCLI(t, "0.1.0-dev")
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
	wizard.waitText(t, "项目目录（")
	declinedDirectory := filepath.Join(t.TempDir(), "declined")
	wizard.write(t, declinedDirectory+"\r")
	wizard.waitText(t, "目录不存在，是否创建目录")
	directoryPrompts := strings.Count(wizard.output.String(), "项目目录（")
	wizard.write(t, "\r") // Default is no.
	wizard.waitTextAfter(t, "项目目录（", directoryPrompts)
	if _, err := os.Stat(declinedDirectory); !os.IsNotExist(err) {
		t.Fatal("declined directory was created", err)
	}
	wizard.write(t, directory+"\r")
	finishProjectWizard(t, wizard)
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
	run("", 0, "ls", "--server", "fixture")
	run("", 0, "ls", "--server", "fixture")
	run("", 2, "ls", "--server", "missing")
	run("", 2, "create", "noninteractive")
	missingDirectory := filepath.Join(t.TempDir(), "missing")
	run("", 2, "create", "fixture/noninteractive", "--directory", missingDirectory)
	if _, err := os.Stat(missingDirectory); !os.IsNotExist(err) {
		t.Fatal("noninteractive create made a directory", err)
	}
	run("", 0, "create", "fixture/second", "--directory", directory)
	run("", 4, "create", "fixture/second", "--directory", directory)
	p, _ = store.Get("second")
	p.Windows = append(p.Windows, config.Window{ID: "api", Name: "接口🙂", Directory: ".", Command: "printf '你好\\n'"})
	draft, _ := json.Marshal(p)
	run(string(draft), 0, "edit", "fixture/second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	run(string(draft), 4, "edit", "fixture/second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	p, _ = store.Get("second")
	l, _ := config.ParseForward("L", "8080:localhost:8000")
	r, _ := config.ParseForward("R", "9000:localhost:3000")
	p.Forwards = []config.Forward{l, r}
	draft, _ = json.Marshal(p)
	run(string(draft), 0, "edit", "second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	p, err = store.Get("second")
	if err != nil || len(p.Forwards) != 2 || p.Forwards[1].Direction != "R" {
		t.Fatal(p, err)
	}
	run("", 0, "override", "second", "--forward", p.Forwards[0].ID, "--local-port", "8081")
	run("", 0, "show", "second")
	newDirectory := t.TempDir()
	run("", 0, "edit", "second", "--directory", newDirectory)
	p, _ = store.Get("second")
	if p.Directory != newDirectory || p.Forwards[0].ListenPort != 8080 {
		t.Fatal("update wrote client override", p)
	}
	p.Windows[1].Name = "新窗口"
	encoded, _ := json.Marshal(p)
	run(string(encoded), 0, "edit", "second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	run(string(encoded), 4, "edit", "second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
	p, _ = store.Get("second")
	p.Forwards = p.Forwards[:1]
	draft, _ = json.Marshal(p)
	run(string(draft), 0, "edit", "second", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))

	// The standalone wizard also targets an explicitly selected saved server.
	create := terminalCLI(t, binary, root, s.Config, "create", "--server", "fixture")
	create.waitText(t, "项目名称（")
	create.write(t, "third\r")
	create.waitText(t, "项目目录（")
	createdDirectory := filepath.Join(t.TempDir(), "中文 项目' $(false)", "nested")
	create.write(t, createdDirectory+"\r")
	create.waitText(t, "目录不存在，是否创建目录")
	create.write(t, "y\r")
	finishProjectWizard(t, create)
	create.waitText(t, "已创建项目 fixture/third")
	create.exit(t, 0)
	created, err := store.Get("third")
	if err != nil || created.Directory != createdDirectory {
		t.Fatal("created directory not saved", created, err)
	}
	if info, err := os.Stat(createdDirectory); err != nil || !info.IsDir() {
		t.Fatal("directory not created on server", err)
	}
	t.Run("cancel_directory_creation", func(t *testing.T) {
		cancelDirectory := filepath.Join(t.TempDir(), "cancelled")
		cancel := terminalCLI(t, binary, root, s.Config, "create", "fixture/cancel-directory")
		cancel.waitText(t, "项目目录（")
		cancel.write(t, cancelDirectory+"\r")
		cancel.waitText(t, "目录不存在，是否创建目录")
		cancel.write(t, "\x03")
		cancel.exit(t, 130)
		if _, err := os.Stat(cancelDirectory); !os.IsNotExist(err) {
			t.Fatal("cancelled confirmation made a directory", err)
		}
		if _, err := store.Get("cancel-directory"); err == nil {
			t.Fatal("cancelled confirmation saved a project")
		}
	})
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
		cancelled.waitText(t, "项目目录（")
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
		opened.waitText(t, "项目目录（")
		opened.write(t, directory+"\r")
		finishProjectWizard(t, opened)
		opened.waitText(t, "正在打开工作区")
		opened.waitText(t, "main*")
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
		choose.write(t, "fixture\r")
		choose.waitText(t, "项目名称（")
		choose.write(t, "selected\r")
		choose.waitText(t, "项目目录（")
		choose.write(t, directory+"\r")
		finishProjectWizard(t, choose)
		choose.waitText(t, "已创建项目 fixture/selected")
		choose.exit(t, 0)
		if _, err := store.Get("selected"); err != nil {
			t.Fatal(err)
		}
		if _, err := (config.Store{Root: otherRoot}).Get("selected"); err == nil {
			t.Fatal("created on default server instead of selected server")
		}
		bare := terminalCLI(t, binary, root, s.Config, "create", "named")
		bare.waitText(t, "项目目录（")
		bare.write(t, directory+"\r")
		finishProjectWizard(t, bare)
		bare.waitText(t, "已创建项目 local/named")
		bare.exit(t, 0)
		if _, err := (config.Store{Root: root}).Get("named"); err != nil {
			t.Fatal(err)
		}
		run("", 0, "create", "unqualified", "--directory", directory)
		run("", 0, "create", "flagged", "--server", "fixture", "--directory", directory)
		cancel := terminalCLI(t, binary, root, s.Config, "create")
		cancel.waitText(t, "选择项目所在服务器")
		cancel.write(t, "q\r")
		cancel.exit(t, 130)
		a := &app{store: config.Store{Root: root}}
		prefs, err := a.prefs()
		if err != nil || len(prefs.Servers) != 2 {
			t.Fatal("selection changed registered servers", err)
		}
		stored, err := os.ReadFile(filepath.Join(root, "client.yaml"))
		if err != nil || strings.Contains(string(stored), "default_server:") {
			t.Fatal("setup persisted a default server", err)
		}
	})
}
