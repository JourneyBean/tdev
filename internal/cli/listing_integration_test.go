package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"tdev/internal/config"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestListShowsCacheBeforeRemoteRefresh(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0-dev")
	root, remoteRoot := t.TempDir(), t.TempDir()
	store := config.Store{Root: remoteRoot}
	if _, err := store.Create(context.Background(), "cached", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "release")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "remote")
	script := "#!/bin/sh\nwhile ! test -f " + tmux.Quote(gate) + "; do sleep 0.1; done\nexec " + tmux.Quote(binary) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", wrapper, "--remote-config-dir", remoteRoot)
	executeBinary(t, binary, root, s.Config, "", 0, "ls", "--server", "dev")
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0600) })
	// Only stdout is a terminal: stdin is closed and stderr goes to a file.
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	cmd := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "ls", "--server", "dev")
	cmd.Stdin, cmd.Stderr = stdin, stderr
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = slave
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		t.Fatal(err)
	}
	_ = slave.Close()
	view := &terminalProcess{cmd: cmd, terminal: master, done: make(chan error, 1)}
	go func() { _, _ = io.Copy(&view.output, master) }()
	go func() { view.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = master.Close() })
	view.waitText(t, "dev [syncing]")
	view.waitText(t, "cached  project")
	select {
	case err := <-view.done:
		t.Fatalf("listing exited before remote was released: %v", err)
	default:
	}
	if _, err := store.Create(context.Background(), "fresh", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(view.output.String(), "fresh   project") {
		t.Fatal("displayed data before remote discovery")
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	view.waitText(t, "fresh   project")
	view.exit(t, 0)
	// Cache files should also include local projects after a refresh.
	localStore := config.Store{Root: root}
	if _, err := localStore.Create(context.Background(), "local-demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "ls", "--server", "local")
	a := &app{store: localStore, sshConfig: s.Config, timeout: time.Second}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.serverTarget(prefs, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	cached, ok := a.loadListingCache(target)
	if !ok || len(cached.Projects) != 1 || cached.Projects[0].Project.Name != "local-demo" {
		t.Fatalf("local cache missing: %+v", cached)
	}
}

func TestListProjectsAcrossServers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0-dev")
	root, populatedRoot, emptyRoot := t.TempDir(), t.TempDir(), t.TempDir()
	if _, err := (config.Store{Root: populatedRoot}).Create(context.Background(), "demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "populated", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", populatedRoot)
	// An empty server must not hide projects on the other configured server.
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "empty", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", emptyRoot)
	for _, command := range [][]string{{"ls"}} {
		for _, tc := range []struct {
			name string
			args []string
			want map[string]int
		}{
			{"default_all_servers", nil, map[string]int{"local": 0, "empty": 0, "populated": 1}},
			{"explicit_all", []string{"--all"}, map[string]int{"local": 0, "empty": 0, "populated": 1}},
			{"selected_empty", []string{"--server", "empty"}, map[string]int{"empty": 0}},
			{"selected_populated", []string{"--server", "populated"}, map[string]int{"populated": 1}},
		} {
			t.Run(strings.Join(command, "_")+"/"+tc.name, func(t *testing.T) {
				args := append(append([]string{}, command...), tc.args...)
				response := executeBinary(t, binary, root, s.Config, "", 0, args...)
				data, err := json.Marshal(response.Data)
				if err != nil {
					t.Fatal(err)
				}
				var projects map[string]serverListing
				if err = json.Unmarshal(data, &projects); err != nil {
					t.Fatal(err)
				}
				if len(projects) != len(tc.want) {
					t.Fatalf("listed servers: %v, want %v", projects, tc.want)
				}
				for name, count := range tc.want {
					listed, ok := projects[name]
					if !ok || len(listed.Projects) != count {
						t.Fatalf("server %s: %v, want %d projects", name, listed, count)
					}
					if count > 0 && listed.Projects[0].Project.Name != "demo" {
						t.Fatalf("unexpected remote project: %v", listed.Projects[0].Project)
					}
				}
			})
		}
		executeBinary(t, binary, root, s.Config, "", 2, append(append([]string{}, command...), "--server", "missing")...)
		executeBinary(t, binary, root, s.Config, "", 2, append(append([]string{}, command...), "--all", "--server", "empty")...)
	}
	output, err := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "ls").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	for _, text := range []string{"SERVER NAME TYPE STATUS DIR FORWARDS", "empty — — synced —", "populated demo project inactive"} {
		if !strings.Contains(strings.Join(strings.Fields(string(output)), " "), text) {
			t.Fatalf("human listing missing %q: %s", text, output)
		}
	}
	t.Run("unmanaged_sessions_are_warnings", func(t *testing.T) {
		t.Setenv("TMUX", "")
		local := tmux.Manager{Store: config.Store{Root: root}, Socket: "tdev-list-" + config.NewID()}
		t.Setenv("TDEV_TMUX_SOCKET", local.Socket)
		t.Cleanup(func() { _ = exec.Command("tmux", local.Args("kill-server")...).Run() })
		if _, err := local.Store.Create(context.Background(), "demo", t.TempDir()); err != nil {
			t.Fatal(err)
		}
		remote := tmux.Manager{Store: config.Store{Root: populatedRoot}, Socket: s.Socket}
		healthy, err := remote.Store.Create(context.Background(), "healthy", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := remote.Ensure(context.Background(), healthy); err != nil {
			t.Fatal(err)
		}
		for _, manager := range []tmux.Manager{local, remote} {
			if out, err := exec.Command("tmux", manager.Args("new-session", "-d", "-s", "tdev-project-demo")...).CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
		}
		response := executeBinary(t, binary, root, s.Config, "", 0, "ls")
		data, _ := json.Marshal(response.Data)
		var listing projectListing
		if err := json.Unmarshal(data, &listing); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"local", "populated"} {
			entry := listing[name]
			if entry.State != "synced" || entry.Error != "" || len(entry.Projects) == 0 || entry.Projects[0].Session.Status != "unmanaged" || entry.Projects[0].Session.Warning == "" {
				t.Fatalf("unmanaged session failed refresh: %s %+v", name, entry)
			}
		}
		if entry := listing["populated"]; len(entry.Projects) != 2 || entry.Projects[1].Project.Name != "healthy" || entry.Projects[1].Session.Status != "running" {
			t.Fatalf("healthy project missing: %+v", entry)
		}
		output, err := exec.Command(binary, "--config-dir", root, "--ssh-config", s.Config, "ls").CombinedOutput()
		hasWarningRow := false
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 5 && fields[0] == "populated" && fields[1] == "demo" && fields[2] == "project" && fields[3] == "unmanaged,warning" {
				hasWarningRow = true
			}
		}
		if err != nil || strings.Contains(string(output), "warning:") || !hasWarningRow || strings.Count(string(output), "运行 tdev doctor") != 1 || strings.Contains(string(output), "unreachable") {
			t.Fatalf("human warning listing: %v %s", err, output)
		}
		for _, args := range [][]string{{"doctor"}, {"doctor", "populated/demo"}} {
			diagnosis := executeBinary(t, binary, root, s.Config, "", 0, args...)
			encoded, _ := json.Marshal(diagnosis.Data)
			warning, _ := json.Marshal(listing["populated"].Projects[0].Session.Warning)
			if !strings.Contains(string(encoded), string(warning)) {
				t.Fatalf("doctor omitted session warning: %s", encoded)
			}
			humanArgs := append([]string{"--config-dir", root, "--ssh-config", s.Config}, args...)
			human, err := exec.Command(binary, humanArgs...).CombinedOutput()
			if err != nil || !strings.Contains(string(human), "同名 tmux 会话未匹配项目标记") || strings.Contains(string(human), `"client":`) {
				t.Fatalf("human doctor diagnosis: %v %s", err, human)
			}
		}
		open := terminalCLI(t, binary, root, s.Config, "open", "local/demo")
		open.waitText(t, "warning:")
		open.write(t, "printf 'FALLBACK_SESSION_READY\\n'\r")
		open.waitText(t, "FALLBACK_SESSION_READY\r\n")
		open.write(t, "tmux detach-client\r")
		open.exit(t, 0)
		project, err := local.Store.Get("demo")
		if err != nil {
			t.Fatal(err)
		}
		managed, err := local.Status(context.Background(), project)
		if err != nil || managed.Status != "running" || !strings.HasPrefix(managed.SessionName, "tdev-project-") || managed.Warning == "" {
			t.Fatalf("open should create a separate project session: %+v %v", managed, err)
		}
		for _, manager := range []tmux.Manager{local, remote} {
			if err := exec.Command("tmux", manager.Args("has-session", "-t", "=tdev-project-demo")...).Run(); err != nil {
				t.Fatal("unmanaged session was changed", err)
			}
		}
	})
	t.Run("remote_application_error_is_reachable", func(t *testing.T) {
		path := filepath.Join(populatedRoot, "projects", "broken.yaml")
		if err := os.WriteFile(path, []byte("unknown_field: true\n"), 0600); err != nil {
			t.Fatal(err)
		}
		response := executeBinary(t, binary, root, s.Config, "", 9, "ls")
		data, _ := json.Marshal(response.Error.Details)
		var listing projectListing
		if err := json.Unmarshal(data, &listing); err != nil {
			t.Fatal(err)
		}
		if entry := listing["populated"]; entry.State != "error" || entry.Error == "" || len(entry.Projects) != 2 {
			t.Fatalf("application error should preserve cached projects: %+v", entry)
		}
	})
}
