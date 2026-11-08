package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestProjectOperationsDoNotQuerySessionStatus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0")
	root, remoteRoot, emptyRoot, directory := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	store := config.Store{Root: remoteRoot}
	if _, err := store.Create(context.Background(), "demo", directory); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), "unrelated", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	payload := []byte("updated client binary\n")
	if err := os.Mkdir(filepath.Join(directory, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "dist", "tdev-darwin-arm64"), payload, 0700); err != nil {
		t.Fatal(err)
	}
	// Emulate the older API: definitions and transfers work, but listing session
	// status fails on an unrelated project's unmanaged same-name session.
	wrapper := filepath.Join(t.TempDir(), "old-server")
	failure := `{"protocol_version":1,"ok":false,"error":{"code":"SESSION_CONFLICT","message":"session identity mismatch for an unrelated project"}}`
	script := "#!/bin/sh\ncase \"$*\" in\n*'api session add-window '*) exec " + tmux.Quote(binary) + " \"$@\";;\n*'project list --status'*|*'api session '*) printf '%s\\n' " + tmux.Quote(failure) + "; exit 4;;\nesac\nexec " + tmux.Quote(binary) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	prefs := defaultPreferences()
	prefs.Servers["a-empty"] = server{SSHAlias: s.Alias, RemoteBinary: wrapper, RemoteConfigDir: emptyRoot}
	prefs.Servers["dev"] = server{SSHAlias: s.Alias, RemoteBinary: wrapper, RemoteConfigDir: remoteRoot}
	data, err := yaml.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Prove this remote still returns the old status error; the failed refresh
	// provides no cached project name that could bypass live resolution.
	executeBinary(t, binary, root, s.Config, "", 9, "ls", "--server", "dev")
	for _, project := range []string{"demo", "dev/demo"} {
		t.Run(project, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "tdev")
			executeBinary(t, binary, root, s.Config, "", 0, "pull", project, "dist/tdev-darwin-arm64", destination)
			if data, err := os.ReadFile(destination); err != nil || string(data) != string(payload) {
				t.Fatalf("pull payload: %q %v", data, err)
			}
			executeBinary(t, binary, root, s.Config, "", 0, "push", project, destination, "uploaded-client", "-y")
			if data, err := os.ReadFile(filepath.Join(directory, "uploaded-client")); err != nil || string(data) != string(payload) {
				t.Fatalf("push payload: %q %v", data, err)
			}
		})
	}
	t.Run("definitions_and_completion", func(t *testing.T) {
		executeBinary(t, binary, root, s.Config, "", 0, "show", "demo")
		executeBinary(t, binary, root, s.Config, "", 0, "override", "demo")
		executeBinary(t, binary, root, s.Config, "", 0, "create", "dev/config-only", "--directory", directory)
		executeBinary(t, binary, root, s.Config, "", 0, "edit", "config-only", "--directory", directory)
		executeBinary(t, binary, root, s.Config, "", 0, "forward", "add", "config-only", "-L", "8080:localhost:8000")
		project, err := store.Get("config-only")
		if err != nil || len(project.Forwards) != 1 {
			t.Fatal("forward definition", project, err)
		}
		id := project.Forwards[0].ID
		executeBinary(t, binary, root, s.Config, "", 0, "override", "config-only", "--forward", id, "--local-port", "18080")
		executeBinary(t, binary, root, s.Config, "", 0, "forward", "remove", "config-only", "--id", id)
		output, err := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "__complete", "show", "dev/d").CombinedOutput()
		if err != nil || !strings.Contains(string(output), "dev/demo") {
			t.Fatalf("project completion: %v %s", err, output)
		}
	})
	t.Run("setup_definitions_without_status", func(t *testing.T) {
		response := executeBinary(t, binary, t.TempDir(), s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", wrapper, "--remote-config-dir", remoteRoot)
		data, _ := json.Marshal(response.Data)
		var result struct {
			Projects []discoveredProject `json:"projects"`
		}
		if err := json.Unmarshal(data, &result); err != nil || len(result.Projects) == 0 {
			t.Fatalf("setup projects: %s %v", data, err)
		}
		for _, project := range result.Projects {
			if project.Session.Status != "unknown" {
				t.Fatalf("setup should leave session status unknown: %+v", project.Session)
			}
		}
		executeBinary(t, binary, root, s.Config, "", 0, "doctor", "demo")
	})
	t.Run("setup_create_without_status", func(t *testing.T) {
		wizardRoot, wizardRemote := t.TempDir(), t.TempDir()
		executeBinary(t, binary, wizardRoot, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", wrapper, "--remote-config-dir", wizardRemote)
		wizard := terminalCLI(t, binary, wizardRoot, s.Config, "setup", "--interactive")
		wizard.waitText(t, "输入序号")
		wizard.write(t, "1\r")
		wizard.waitText(t, "现在添加第一个项目")
		wizard.write(t, "y\r")
		wizard.waitText(t, "项目名称（")
		wizard.write(t, "first\r")
		wizard.waitText(t, "项目目录（")
		wizard.write(t, directory+"\r")
		finishProjectWizard(t, wizard)
		wizard.waitText(t, "已创建项目 dev/first")
		wizard.exit(t, 0)
		if _, err := (config.Store{Root: wizardRemote}).Get("first"); err != nil {
			t.Fatal("setup did not save first project", err)
		}
	})
	t.Run("json_shell_without_extra_status_query", func(t *testing.T) {
		manager := tmux.Manager{Store: store, Socket: s.Socket}
		project, err := store.Get("demo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Ensure(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		executeBinary(t, binary, root, s.Config, "", 0, "sh", "demo", "--name", "debug")
		windows, err := exec.Command("tmux", manager.Args("list-windows", "-t", "="+tmux.SessionName(project), "-F", "#{window_name}")...).CombinedOutput()
		if err != nil || !strings.Contains(string(windows), "debug") {
			t.Fatalf("shell window: %v %s", err, windows)
		}
	})
}
